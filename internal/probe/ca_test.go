package probe

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtraRootsAndHint(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	run := func(skip bool) Result {
		return RunX(context.Background(), Spec{Type: "http", Target: srv.URL, Timeout: 5 * time.Second, HTTP: HTTPOptions{SkipTLSVerify: skip}}, false)
	}
	ExtraRoots = nil
	r := run(false)
	if r.Up || !strings.Contains(r.Err.Error(), "APM_CA_FILE") {
		t.Fatalf("güvenilmeyen CA reddedilmeli ve ipucu içermeli: %+v", r)
	}
	if !run(true).Up {
		t.Fatal("doğrulama kapalıyken başarılı olmalı")
	}
	// kurum CA dosyası ile
	pemPath := filepath.Join(t.TempDir(), "ca.pem")
	var pem []byte
	for _, c := range srv.TLS.Certificates[0].Certificate {
		x, _ := x509.ParseCertificate(c)
		pem = append(pem, []byte("-----BEGIN CERTIFICATE-----\n")...)
		pem = append(pem, []byte(b64(x.Raw))...)
		pem = append(pem, []byte("-----END CERTIFICATE-----\n")...)
	}
	os.WriteFile(pemPath, pem, 0o600)
	n, err := LoadCAFile(pemPath)
	defer func() { ExtraRoots = nil }()
	if err != nil || n < 1 {
		t.Fatal(n, err)
	}
	if r := run(false); !r.Up {
		t.Fatalf("CA yüklenince başarılı olmalı: %v", r.Err)
	}
	if _, err := LoadCAFile(filepath.Join(t.TempDir(), "yok.pem")); err == nil {
		t.Fatal("olmayan dosya hata vermeli")
	}
}

func b64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 64 {
		out.WriteString(s[:64] + "\n")
		s = s[64:]
	}
	return out.String() + s + "\n"
}
