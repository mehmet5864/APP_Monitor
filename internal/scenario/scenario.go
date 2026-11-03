// Package scenario çok adımlı HTTP "bot" senaryolarını çalıştırır: oturum çerezleri adımlar arasında korunur,
// bir adımın yanıtından değer çıkarılıp sonraki adımlarda {{degisken}} olarak kullanılır.
package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"appmon/internal/probe"
)

const (
	MaxSteps   = 20
	maxBody    = 8192
	maxExtract = 5
)

// Extract bir yanıttan değer çıkarır. From: "json" (Path: data.token veya items.0.id), "header" (Path: başlık adı)
// veya "regex" (Path: düzenli ifade; varsa ilk grup, yoksa tüm eşleşme).
type Extract struct {
	Var  string `json:"var"`
	From string `json:"from"`
	Path string `json:"path"`
}

type Step struct {
	Name         string            `json:"name"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	ExpectStatus string            `json:"expect_status,omitempty"`
	ExpectText   string            `json:"expect_text,omitempty"`
	MaxMS        int               `json:"max_ms,omitempty"`
	Extract      []Extract         `json:"extract,omitempty"`
}

type Result struct {
	Name   string  `json:"name"`
	OK     bool    `json:"ok"`
	Status int     `json:"status,omitempty"`
	MS     float64 `json:"ms"`
	Error  string  `json:"error,omitempty"`
}

type Options struct {
	Timeout       time.Duration // her istek için
	BlockPrivate  bool
	SkipTLSVerify bool
	Vars          map[string]string
}

var (
	varRef  = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
	varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)
)

func refs(s string) []string {
	var out []string
	for _, m := range varRef.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// Normalize adımları kaydetmeden önce düzenler: yöntemi büyük harfe çevirir, boşlukları kırpar.
func Normalize(steps []Step) {
	for i := range steps {
		s := &steps[i]
		s.Name = strings.TrimSpace(s.Name)
		s.Method = strings.ToUpper(strings.TrimSpace(s.Method))
		if s.Method == "" {
			s.Method = "GET"
		}
		s.URL = strings.TrimSpace(s.URL)
		s.ExpectStatus = strings.TrimSpace(s.ExpectStatus)
		for j := range s.Extract {
			s.Extract[j].Var = strings.TrimSpace(s.Extract[j].Var)
			s.Extract[j].From = strings.TrimSpace(s.Extract[j].From)
			s.Extract[j].Path = strings.TrimSpace(s.Extract[j].Path)
		}
	}
}

// Validate senaryoyu çalıştırmadan önce yapısal hataları yakalar; her {{değişken}} kullanılmadan önce tanımlı olmalıdır.
func Validate(steps []Step, vars map[string]string) error {
	if len(steps) == 0 || len(steps) > MaxSteps {
		return fmt.Errorf("senaryo 1 ile %d arasında adım içermeli", MaxSteps)
	}
	if len(vars) > 30 {
		return errors.New("en fazla 30 değişken tanımlanabilir")
	}
	defined := map[string]bool{}
	for k, v := range vars {
		if !varName.MatchString(k) {
			return fmt.Errorf("değişken adı geçersiz: %q (harf, rakam ve _ kullanın, harfle başlayın)", k)
		}
		if len(v) > 2000 {
			return fmt.Errorf("%s değişkeninin değeri çok uzun", k)
		}
		defined[k] = true
	}
	for i, st := range steps {
		n := i + 1
		if st.Name == "" || len(st.Name) > 100 {
			return fmt.Errorf("adım %d: ad zorunlu ve en fazla 100 karakter olmalı", n)
		}
		if !probe.ValidMethod(st.Method) {
			return fmt.Errorf("adım %d: yöntem %s olmalı", n, strings.Join(probe.AllowedMethods, ", "))
		}
		if len(st.URL) > 2000 || !(strings.HasPrefix(st.URL, "http://") || strings.HasPrefix(st.URL, "https://")) {
			return fmt.Errorf("adım %d: adres http:// veya https:// ile başlamalı", n)
		}
		if !strings.Contains(st.URL, "{{") {
			if u, err := url.Parse(st.URL); err != nil || u.Host == "" {
				return fmt.Errorf("adım %d: adres geçersiz", n)
			}
		}
		if len(st.Body) > maxBody {
			return fmt.Errorf("adım %d: gövde en fazla %d karakter olabilir", n, maxBody)
		}
		if len(st.ExpectText) > 200 {
			return fmt.Errorf("adım %d: beklenen metin en fazla 200 karakter olabilir", n)
		}
		if st.Method == "HEAD" && st.ExpectText != "" {
			return fmt.Errorf("adım %d: HEAD yanıtında gövde olmadığı için metin doğrulaması yapılamaz", n)
		}
		if st.MaxMS < 0 || st.MaxMS > 600000 {
			return fmt.Errorf("adım %d: süre sınırı 0 ile 600000 ms arasında olmalı", n)
		}
		if err := probe.ValidateStatusSpec(st.ExpectStatus); err != nil {
			return fmt.Errorf("adım %d: %w", n, err)
		}
		if err := probe.ValidateHeaders(st.Headers); err != nil {
			return fmt.Errorf("adım %d: %w", n, err)
		}
		used := refs(st.URL)
		used = append(used, refs(st.Body)...)
		for _, v := range st.Headers {
			used = append(used, refs(v)...)
		}
		for _, name := range used {
			if !defined[name] {
				return fmt.Errorf("adım %d: {{%s}} değişkeni tanımlı değil (önceki bir adımdan çıkarılmalı veya senaryo değişkeni olmalı)", n, name)
			}
		}
		if len(st.Extract) > maxExtract {
			return fmt.Errorf("adım %d: en fazla %d değer çıkarılabilir", n, maxExtract)
		}
		for _, e := range st.Extract {
			if !varName.MatchString(e.Var) {
				return fmt.Errorf("adım %d: değişken adı geçersiz: %q", n, e.Var)
			}
			if e.Path == "" || len(e.Path) > 200 {
				return fmt.Errorf("adım %d: %s için yol/ifade zorunlu", n, e.Var)
			}
			switch e.From {
			case "json", "header":
			case "regex":
				if _, err := regexp.Compile(e.Path); err != nil {
					return fmt.Errorf("adım %d: düzenli ifade geçersiz: %v", n, err)
				}
			default:
				return fmt.Errorf("adım %d: çıkarma kaynağı json, header veya regex olmalı", n)
			}
		}
		for _, e := range st.Extract {
			defined[e.Var] = true
		}
	}
	return nil
}

func subst(s string, vars map[string]string) (string, error) {
	var missing string
	out := varRef.ReplaceAllStringFunc(s, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("{{%s}} değişkeni tanımsız", missing)
	}
	return out, nil
}

func buildRequest(st Step, vars map[string]string) (probe.HTTPRequest, error) {
	u, err := subst(st.URL, vars)
	if err != nil {
		return probe.HTTPRequest{}, err
	}
	body, err := subst(st.Body, vars)
	if err != nil {
		return probe.HTTPRequest{}, err
	}
	headers := make(map[string]string, len(st.Headers))
	for k, v := range st.Headers {
		sv, err := subst(v, vars)
		if err != nil {
			return probe.HTTPRequest{}, err
		}
		if strings.ContainsAny(sv, "\r\n") {
			return probe.HTTPRequest{}, fmt.Errorf("%s başlığının değeri satır sonu içeriyor", k)
		}
		headers[k] = sv
	}
	return probe.HTTPRequest{Method: st.Method, URL: u, Headers: headers, Body: body, ExpectStatus: st.ExpectStatus,
		ExpectText: st.ExpectText, NeedBody: len(st.Extract) > 0}, nil
}

func jsonPath(data []byte, path string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", errors.New("yanıt geçerli JSON değil")
	}
	for _, seg := range strings.Split(path, ".") {
		switch t := v.(type) {
		case map[string]any:
			next, ok := t[seg]
			if !ok {
				return "", fmt.Errorf("JSON yolu bulunamadı: %s", path)
			}
			v = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(t) {
				return "", fmt.Errorf("JSON yolu bulunamadı: %s", path)
			}
			v = t[idx]
		default:
			return "", fmt.Errorf("JSON yolu bulunamadı: %s", path)
		}
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case json.Number:
		return t.String(), nil
	case bool:
		return strconv.FormatBool(t), nil
	default:
		return "", fmt.Errorf("JSON yolu bir metin, sayı veya mantıksal değere işaret etmeli: %s", path)
	}
}

func extract(list []Extract, resp probe.HTTPResponse, vars map[string]string) error {
	for _, e := range list {
		var val string
		switch e.From {
		case "json":
			v, err := jsonPath(resp.Body, e.Path)
			if err != nil {
				return fmt.Errorf("%s çıkarılamadı: %w", e.Var, err)
			}
			val = v
		case "header":
			val = resp.Header.Get(e.Path)
			if val == "" {
				return fmt.Errorf("%s çıkarılamadı: %s başlığı yanıtta yok", e.Var, e.Path)
			}
		case "regex":
			re, err := regexp.Compile(e.Path)
			if err != nil {
				return fmt.Errorf("%s çıkarılamadı: düzenli ifade geçersiz", e.Var)
			}
			m := re.FindSubmatch(resp.Body)
			if m == nil {
				return fmt.Errorf("%s çıkarılamadı: düzenli ifade eşleşmedi", e.Var)
			}
			if len(m) > 1 {
				val = string(m[1])
			} else {
				val = string(m[0])
			}
		}
		vars[e.Var] = val
	}
	return nil
}

// Run adımları sırayla çalıştırır; ilk başarısız adımda durur. Hata mesajları yanıt gövdesini veya
// çıkarılan değerleri (ör. token) içermez.
func Run(ctx context.Context, steps []Step, o Options) ([]Result, time.Duration, error) {
	jar, _ := cookiejar.New(nil)
	client := probe.NewClient(o.Timeout, o.BlockPrivate, o.SkipTLSVerify, true, jar)
	vars := make(map[string]string, len(o.Vars))
	for k, v := range o.Vars {
		vars[k] = v
	}
	results := make([]Result, 0, len(steps))
	start := time.Now()
	for i, st := range steps {
		res := Result{Name: st.Name}
		fail := func(err error) ([]Result, time.Duration, error) {
			res.Error = err.Error()
			results = append(results, res)
			return results, time.Since(start), fmt.Errorf("adım %d (%s): %w", i+1, st.Name, err)
		}
		req, err := buildRequest(st, vars)
		if err != nil {
			return fail(err)
		}
		resp, err := probe.DoHTTP(ctx, client, req)
		res.Status = resp.Status
		res.MS = float64(resp.Duration.Microseconds()) / 1000
		if err == nil && st.MaxMS > 0 && resp.Duration > time.Duration(st.MaxMS)*time.Millisecond {
			err = fmt.Errorf("yanıt süresi sınırı aşıldı (%.0f ms > %d ms)", res.MS, st.MaxMS)
		}
		if err == nil {
			err = extract(st.Extract, resp, vars)
		}
		if err != nil {
			return fail(err)
		}
		res.OK = true
		results = append(results, res)
	}
	return results, time.Since(start), nil
}
