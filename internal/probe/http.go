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
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLSVerify}, //nolint:gosec
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
	NeedBody     bool // değişken çıkarma için gövdeyi saklar
}

type HTTPResponse struct {
	Status   int
	Header   http.Header
	Body     []byte
	Duration time.Duration
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

	if !StatusMatches(r.ExpectStatus, resp.StatusCode) {
		return res, fmt.Errorf("beklenmeyen HTTP durum kodu %d (beklenen: %s)", resp.StatusCode, specText(r.ExpectStatus))
	}
	if r.ExpectText != "" && !bytes.Contains(data, []byte(r.ExpectText)) {
		return res, errors.New("yanıtta beklenen metin bulunamadı")
	}
	return res, nil
}
