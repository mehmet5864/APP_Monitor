package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidOffer(t *testing.T) {
	good := &updateOffer{Name: expectedAgentName(), SHA256: hex.EncodeToString(make([]byte, 32)), Size: 10}
	if err := validOffer(good); err != nil {
		t.Fatal(err)
	}
	for _, o := range []*updateOffer{nil, {Name: "../etc/passwd", SHA256: good.SHA256}, {Name: "appmon-agent-linux-amd64x", SHA256: good.SHA256},
		{Name: expectedAgentName(), SHA256: "xyz"}, {Name: expectedAgentName(), SHA256: good.SHA256, Size: maxAgentBin + 1}} {
		if validOffer(o) == nil {
			t.Fatalf("geçersiz teklif kabul edildi: %+v", o)
		}
	}
	other := "appmon-agent-windows-arm64.exe"
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		other = "appmon-agent-linux-amd64"
	}
	if validOffer(&updateOffer{Name: other, SHA256: good.SHA256}) == nil {
		t.Fatal("başka platformun dosyası reddedilmeli")
	}
}

func TestDecodeOffer(t *testing.T) {
	sha := hex.EncodeToString(make([]byte, 32))
	if o := decodeOffer([]byte(`{"asset_id":1,"update":{"name":"` + expectedAgentName() + `","sha256":"` + sha + `","size":5}}`)); o == nil || o.SHA256 != sha {
		t.Fatalf("teklif okunamadı: %+v", o)
	}
	if decodeOffer([]byte(`{"asset_id":1}`)) != nil || decodeOffer([]byte(`not json`)) != nil {
		t.Fatal("teklif yokken nil beklenir")
	}
}

func TestDownloadVerified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kabuk betiği gerekir")
	}
	script := []byte("#!/bin/sh\necho appmon-agent test\n")
	sum := sha256.Sum256(script)
	good := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.Write([]byte("#!/bin/sh\nexit 1\n"))
			return
		}
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write(script)
	}))
	defer srv.Close()
	dir := t.TempDir()
	dst := filepath.Join(dir, "new")
	if err := downloadVerified(srv.Client(), srv.URL+"/ok", dst, good); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dst); err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("çalıştırılabilir dosya beklenir: %v", err)
	}
	if err := downloadVerified(srv.Client(), srv.URL+"/ok", dst, hex.EncodeToString(make([]byte, 32))); err == nil {
		t.Fatal("yanlış SHA-256 reddedilmeli")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("hatalı dosya silinmeli")
	}
	bad := sha256.Sum256([]byte("#!/bin/sh\nexit 1\n"))
	if err := downloadVerified(srv.Client(), srv.URL+"/bad", dst, hex.EncodeToString(bad[:])); err == nil {
		t.Fatal("çalışmayan ikili reddedilmeli")
	}
	if err := downloadVerified(srv.Client(), srv.URL+"/missing", dst, good); err == nil {
		t.Fatal("404 hata olmalı")
	}
}
