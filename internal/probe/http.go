package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxResponseBytes = 1 << 20 // yanıt gövdesinden en fazla 1 MB okunur

var AllowedMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

func ValidMethod(m string) bool {
	for _, a := range AllowedMethods {
		if m == a {
			return true
		}
	}
	return false
}

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

// ValidateHeaders istek başlıklarının sayısını, adlarını ve değerlerini denetler.
func ValidateHeaders(h map[string]string) error {
	if len(h) > 20 {
		return errors.New("en fazla 20 başlık tanımlanabilir")
	}
	for k, v := range h {
		if !headerName.MatchString(k) {
			return fmt.Errorf("geçersiz başlık adı: %q", k)
		}
		if len(v) > 2000 || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%s başlığının değeri geçersiz (en fazla 2000 karakter, satır sonu içeremez)", k)
		}
	}
	return nil
}

type statusRange struct{ lo, hi int }

// ParseStatusSpec "200", "200-299" veya "200,204,301-302" biçimini çözer. Boşsa 200-399 kabul edilir.
func ParseStatusSpec(spec string) ([]statusRange, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return []statusRange{{200, 399}}, nil
	}
	var out []statusRange
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi := 0, 0
		var err1, err2 error
		if a, b, ok := strings.Cut(part, "-"); ok {
			lo, err1 = strconv.Atoi(strings.TrimSpace(a))
			hi, err2 = strconv.Atoi(strings.TrimSpace(b))
		} else {
			lo, err1 = strconv.Atoi(part)
			hi, err2 = lo, err1
		}
		if err1 != nil || err2 != nil || lo < 100 || hi > 599 || lo > hi {
			return nil, fmt.Errorf("beklenen durum kodu geçersiz (örnek: 200, 200-299 veya 200,204): %q", spec)
		}
		out = append(out, statusRange{lo, hi})
	}
	return out, nil
}

func ValidateStatusSpec(spec string) error {
	_, err := ParseStatusSpec(spec)
	return err
}

func StatusMatches(spec string, code int) bool {
	ranges, err := ParseStatusSpec(spec)
	if err != nil {
		return false
	}
	for _, r := range ranges {
		if code >= r.lo && code <= r.hi {
			return true
		}
	}
	return false
}

func specText(spec string) string {
	if strings.TrimSpace(spec) == "" {
		return "200-399"
	}
	return strings.TrimSpace(spec)
}

// NewClient SaaS koruması (özel ağ engeli) ve ayarlanabilir TLS/yönlendirme davranışıyla bir HTTP istemcisi üretir.
// jar verilirse çerezler adımlar arasında korunur.
func NewClient(timeout time.Duration, blockPrivate, skipTLSVerify, follow bool, jar http.CookieJar) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, Control: guard(blockPrivate)}
	tr := &http.Transport{
		DialContext:         dialer.DialContext,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: timeout,
		// Kullanıcı, kendi iç ağındaki kendinden imzalı sertifikalar için bunu probe başına bilinçli olarak açabilir.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLSVerify, RootCAs: ExtraRoots}, //nolint:gosec
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		Jar:       jar,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
			if len(via) >= 5 {
				return errors.New("çok fazla yönlendirme")
			}
			return nil
		},
	}
}

type HTTPRequest struct {
	Method       string
	URL          string
	Headers      map[string]string
	Body         string
	ExpectStatus string
	ExpectText   string
	NeedBody     bool        // değişken çıkarma için gövdeyi saklar
	Trace        *HTTPTiming // doluysa DNS/TCP/TLS/ilk bayt süreleri kaydedilir
}

// HTTPTiming bir HTTP isteğinin aşamalarını ölçer (httptrace). Yönlendirmelerde DNS/TCP/TLS toplanır;
// ilk bayt ve indirme süresi son isteğe aittir.
type HTTPTiming struct {
	mu                   sync.Mutex
	dns, connect, tls    time.Duration
	dnsAt, connAt, tlsAt time.Time
	wrote, first         time.Time
	got                  bool
}

func (t *HTTPTiming) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.mu.Lock(); t.dnsAt = time.Now(); t.mu.Unlock() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			if !t.dnsAt.IsZero() {
				t.dns += time.Since(t.dnsAt)
			}
			t.mu.Unlock()
		},
		ConnectStart: func(_, _ string) { t.mu.Lock(); t.connAt = time.Now(); t.mu.Unlock() },
		ConnectDone: func(_, _ string, _ error) {
			t.mu.Lock()
			if !t.connAt.IsZero() {
				t.connect += time.Since(t.connAt)
			}
			t.mu.Unlock()
		},
		TLSHandshakeStart: func() { t.mu.Lock(); t.tlsAt = time.Now(); t.mu.Unlock() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			if !t.tlsAt.IsZero() {
				t.tls += time.Since(t.tlsAt)
			}
			t.mu.Unlock()
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.mu.Lock(); t.wrote = time.Now(); t.mu.Unlock() },
		GotFirstResponseByte: func() { t.mu.Lock(); t.first = time.Now(); t.got = true; t.mu.Unlock() },
	}
}

// Millis aşamaları milisaniye olarak döndürür (yanıt hiç alınmadıysa nil). total, isteğin toplam süresidir.
func (t *HTTPTiming) Millis(total time.Duration) map[string]float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.got {
		return nil
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	ttfb := time.Duration(0)
	if !t.wrote.IsZero() && t.first.After(t.wrote) {
		ttfb = t.first.Sub(t.wrote)
	}
	transfer := total - t.dns - t.connect - t.tls - ttfb
	if transfer < 0 {
		transfer = 0
	}
	return map[string]float64{"dns": ms(t.dns), "connect": ms(t.connect), "tls": ms(t.tls), "ttfb": ms(ttfb), "transfer": ms(transfer), "total": ms(total)}
}

type HTTPResponse struct {
	Status   int
	Header   http.Header
	Body     []byte
	Duration time.Duration
	TLS      string // "TLS 1.2" gibi; TLS kullanılmadıysa boş
	Proto    string // "HTTP/1.1", "HTTP/2.0"
}

// DoHTTP isteği gönderir, durum kodunu ve (varsa) yanıt metnini doğrular.
// Hata mesajlarına adres (sorgu parametrelerindeki gizli değerler olabilir) ve yanıt gövdesi eklenmez.
func DoHTTP(ctx context.Context, client *http.Client, r HTTPRequest) (HTTPResponse, error) {
	var res HTTPResponse
	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return res, errors.New("geçersiz istek adresi")
	}
	if r.Trace != nil {
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), r.Trace.clientTrace()))
	}
	req.Header.Set("User-Agent", "APP_MON-probe/1.0")
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		res.Duration = time.Since(start)
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return res, err
	}
	defer resp.Body.Close()
	var data []byte
	if r.NeedBody || r.ExpectText != "" {
		data, _ = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	} else {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	}
	res.Duration = time.Since(start)
	res.Status, res.Header, res.Body = resp.StatusCode, resp.Header, data
	res.TLS = tlsName(resp)
	res.Proto = resp.Proto

	if !StatusMatches(r.ExpectStatus, resp.StatusCode) {
		return res, fmt.Errorf("beklenmeyen HTTP durum kodu %d (beklenen: %s)", resp.StatusCode, specText(r.ExpectStatus))
	}
	if r.ExpectText != "" && !bytes.Contains(data, []byte(r.ExpectText)) {
		return res, errors.New("yanıtta beklenen metin bulunamadı")
	}
	return res, nil
}

func tlsName(resp *http.Response) string {
	if resp.TLS == nil {
		return ""
	}
	switch resp.TLS.Version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return "TLS ?"
}

// HTTPSurface bir HTTP yanıtından çıkarılan, saldırı yüzeyini ilgilendiren özelliklerdir.
type HTTPSurface struct {
	TLS     string   // "TLS 1.3"; boşsa TLS yok
	Headers []string // mevcut güvenlik başlıkları (küçük harf)
	Banner  string   // Server başlığı (en çok 80 karakter)
}

var secHeaders = []string{"Strict-Transport-Security", "Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"}

// SurfaceOf yanıttan HTTPSurface üretir.
func SurfaceOf(r HTTPResponse) HTTPSurface {
	s := HTTPSurface{TLS: r.TLS}
	for _, h := range secHeaders {
		if r.Header.Get(h) != "" {
			s.Headers = append(s.Headers, strings.ToLower(h))
		}
	}
	b := strings.TrimSpace(r.Header.Get("Server"))
	if len(b) > 80 {
		b = b[:80]
	}
	s.Banner = b
	return s
}
