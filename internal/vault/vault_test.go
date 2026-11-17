package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	v, _ := New(bytes.Repeat([]byte{7}, 32))
	blob := v.Seal([]byte("gizli-anahtar"), "s:1")
	if bytes.Contains(blob, []byte("gizli")) {
		t.Fatal("şifreli veri düz metin içeriyor")
	}
	p, err := v.Open(blob, "s:1")
	if err != nil || string(p) != "gizli-anahtar" {
		t.Fatalf("p=%q err=%v", p, err)
	}
	if _, err := v.Open(blob, "s:2"); err == nil {
		t.Fatal("farklı AAD ile çözülmemeliydi (kayıt taşınamaz)")
	}
	w, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := w.Open(blob, "s:1"); err == nil {
		t.Fatal("yanlış anahtarla çözülmemeliydi")
	}
	blob[len(blob)-1] ^= 1
	if _, err := v.Open(blob, "s:1"); err == nil {
		t.Fatal("bozulmuş veri reddedilmeliydi")
	}
	if a, b := v.Seal([]byte("x"), "a"), v.Seal([]byte("x"), "a"); bytes.Equal(a, b) {
		t.Fatal("nonce tekrar kullanılıyor")
	}
}

func TestLoadSourcesAndGeneration(t *testing.T) {
	dir := t.TempDir()
	def := filepath.Join(dir, "yok.key")
	// ortam değişkeni
	k := GenerateKey()
	v, err := Load(Config{Env: k, DefaultFile: def})
	if err != nil || v.Source != "env" {
		t.Fatalf("env: %v %v", v, err)
	}
	// hex
	if _, err := Load(Config{Env: strings.Repeat("ab", 32), DefaultFile: def}); err != nil {
		t.Fatalf("hex: %v", err)
	}
	// kötü anahtar
	if _, err := Load(Config{Env: "kisa", DefaultFile: def}); err == nil {
		t.Fatal("kısa anahtar reddedilmeliydi")
	}
	// anahtar yok, veri dizini yok -> devre dışı
	if _, err := Load(Config{DefaultFile: def}); err != ErrDisabled {
		t.Fatalf("beklenen ErrDisabled, gelen %v", err)
	}
	// veri dizininde üretim, sonra aynı anahtar okunur
	data := filepath.Join(dir, "data")
	g, err := Load(Config{DefaultFile: def, DataDir: data})
	if err != nil || !g.Generated || g.Source != "generated" {
		t.Fatalf("üretim: %+v %v", g, err)
	}
	if st, _ := os.Stat(filepath.Join(data, "master.key")); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("anahtar dosyası 0600 olmalı: %v", st)
	}
	g2, err := Load(Config{DefaultFile: def, DataDir: data})
	if err != nil || g2.Generated || g2.KeyID != g.KeyID {
		t.Fatalf("ikinci açılış aynı anahtarı kullanmalı: %+v %v", g2, err)
	}
	// açıkça verilen dosya yoksa hata
	if _, err := Load(Config{File: filepath.Join(dir, "olmayan"), DefaultFile: def, DataDir: data}); err == nil {
		t.Fatal("olmayan APM_MASTER_KEY_FILE hata vermeli")
	}
	// ortam değişkeni dosyadan önceliklidir
	if e, _ := Load(Config{Env: k, DataDir: data, DefaultFile: def}); e.KeyID == g.KeyID {
		t.Fatal("env önceliği bozuk")
	}
}
