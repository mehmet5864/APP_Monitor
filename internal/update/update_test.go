package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ent struct {
	name string
	data []byte
}

func build(t *testing.T, files []ent) *bytes.Reader {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg})
		tw.Write(f.data)
	}
	tw.Close()
	gz.Close()
	return bytes.NewReader(buf.Bytes())
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func pkg(t *testing.T, ver string, priv ed25519.PrivateKey, mut func(*Manifest, *[]ent)) *bytes.Reader {
	srv := append([]byte("\x7fELF"), []byte("fake-server")...)
	agent := []byte("agent")
	m := Manifest{Format: 1, Product: "appmon", Version: ver, Files: map[string]string{
		"bin/appmon-server-linux-amd64": sum(srv), "bin/appmon-agent-linux-amd64": sum(agent)}}
	files := []ent{{"appmon-guncelleme/bin/appmon-server-linux-amd64", srv}, {"appmon-guncelleme/bin/appmon-agent-linux-amd64", agent},
		{"appmon-guncelleme/guncelle-apache.sh", []byte("#!/bin/sh\nrm -rf /\n")}}
	if mut != nil {
		mut(&m, &files)
	}
	mb, _ := json.Marshal(m)
	files = append(files, ent{"appmon-guncelleme/manifest.json", mb})
	if priv != nil {
		files = append(files, ent{"appmon-guncelleme/manifest.sig", ed25519.Sign(priv, mb)})
	}
	return build(t, files)
}

func TestStageOK(t *testing.T) {
	dir := t.TempDir()
	st, err := Stage(pkg(t, "v1.2.0", nil, nil), dir, "amd64", nil, "v1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "v1.2.0" || st.Signed || len(st.Files) != 2 {
		t.Fatalf("%+v", st)
	}
	sd := filepath.Join(dir, "staged-"+st.ID)
	if b, _ := os.ReadFile(filepath.Join(sd, "SURUM")); strings.TrimSpace(string(b)) != "v1.2.0" {
		t.Fatal("SURUM")
	}
	if b, _ := os.ReadFile(filepath.Join(sd, "SHA256SUMS")); !strings.Contains(string(b), "bin/appmon-server-linux-amd64") {
		t.Fatal("SHA256SUMS")
	}
	if _, err := os.Stat(filepath.Join(sd, "guncelle-apache.sh")); err == nil {
		t.Fatal("pakettaki betik çıkarılmamalı")
	}
	got, _ := Load(dir)
	if got == nil || got.ID != st.ID {
		t.Fatal("Load")
	}
	// ikinci yükleme öncekini siler
	st2, err := Stage(pkg(t, "v1.2.0", nil, nil), dir, "amd64", nil, "v1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sd); err == nil || st2.ID == st.ID {
		t.Fatal("eski hazırlanmış paket silinmeliydi")
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatal("geçici klasör kaldı")
		}
	}
}

func TestStageRejects(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		r    *bytes.Reader
		want string
	}{
		"eski sürüm":  {pkg(t, "v0.9.0", nil, nil), "eski"},
		"bozuk sum":   {pkg(t, "v1.1.0", nil, func(m *Manifest, f *[]ent) { m.Files["bin/appmon-agent-linux-amd64"] = strings.Repeat("0", 64) }), "uyuşmuyor"},
		"fazla dosya": {pkg(t, "v1.1.0", nil, func(m *Manifest, f *[]ent) { *f = append(*f, ent{"bin/evil", []byte("x")}) }), "manifestte olmayan"},
		"arch yok":    {pkg(t, "v1.1.0", nil, func(m *Manifest, f *[]ent) { delete(m.Files, "bin/appmon-server-linux-amd64"); (*f)[0].name = "x/y/z" }), "mimarisi"},
		"elf degil": {pkg(t, "v1.1.0", nil, func(m *Manifest, f *[]ent) {
			b := []byte("script")
			(*f)[0].data = b
			m.Files["bin/appmon-server-linux-amd64"] = sum(b)
		}), "Linux programı"},
		"sürüm":        {pkg(t, "1.1", nil, nil), "geçersiz sürüm"},
		"yol":          {pkg(t, "v1.1.0", nil, func(m *Manifest, f *[]ent) { *f = append(*f, ent{"../../etc/passwd", []byte("x")}) }), "güvenli olmayan"},
		"manifest yok": {build(t, []ent{{"bin/appmon-server-linux-amd64", []byte("x")}}), "manifest.json yok"},
	}
	for name, c := range cases {
		if _, err := Stage(c.r, dir, "amd64", nil, "v1.0.1"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: beklenen %q, gelen %v", name, c.want, err)
		}
	}
	if _, err := Stage(strings.NewReader("duz metin"), dir, "amd64", nil, ""); err == nil {
		t.Error("tar.gz olmayan dosya kabul edildi")
	}
	if st, _ := Load(dir); st != nil {
		t.Error("reddedilen paket hazırlanmış görünüyor")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Errorf("artık dosyalar: %v", ents)
	}
}

func TestSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	if st, err := Stage(pkg(t, "v1.1.0", priv, nil), dir, "amd64", pub, "v1.0.1"); err != nil || !st.Signed {
		t.Fatal(err)
	}
	if _, err := Stage(pkg(t, "v1.1.0", nil, nil), dir, "amd64", pub, "v1.0.1"); err == nil || !strings.Contains(err.Error(), "imzalı değil") {
		t.Fatal("imzasız paket kabul edildi:", err)
	}
	if _, err := Stage(pkg(t, "v1.1.0", other, nil), dir, "amd64", pub, "v1.0.1"); err == nil || !strings.Contains(err.Error(), "imzası geçersiz") {
		t.Fatal("yanlış imza kabul edildi:", err)
	}
}

func TestApplyRequest(t *testing.T) {
	dir := t.TempDir()
	st, err := Stage(pkg(t, "v1.1.0", nil, nil), dir, "amd64", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if RequestApply(dir, "../x", "a") == nil || RequestApply(dir, "0123456789abcdef", "a") == nil {
		t.Fatal("geçersiz kimlik kabul edildi")
	}
	if err := RequestApply(dir, st.ID, "admin"); err != nil || !Pending(dir) {
		t.Fatal(err)
	}
	if RequestApply(dir, st.ID, "admin") == nil {
		t.Fatal("ikinci istek kabul edildi")
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		w    int
	}{{"v1.0.1", "v1.0.0", 1}, {"v1.0.0", "v1.0.0", 0}, {"v1.9.0", "v1.10.0", -1}, {"v2.0.0", "v1.99.99", 1}} {
		if g, _ := Compare(c.a, c.b); g != c.w {
			t.Errorf("%s %s: %d", c.a, c.b, g)
		}
	}
}
