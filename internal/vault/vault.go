// Package vault gizli değerleri (SSH anahtarı, parola, token) AES-256-GCM ile şifreler.
// Ana anahtar veritabanının dışında tutulur: APM_MASTER_KEY (ortam değişkeni), APM_MASTER_KEY_FILE,
// /etc/appmon/master.key veya veri dizinindeki master.key (yoksa ilk açılışta üretilir).
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Vault şifreleme/çözme işlemlerini yapar.
type Vault struct {
	aead      cipher.AEAD
	KeyID     string // anahtarın kısa parmak izi (gizli değildir; yanlış anahtarı ayırt etmek için)
	Source    string // "env", "file" veya "generated"
	Path      string // anahtar dosyasının yolu (varsa)
	Generated bool   // bu açılışta otomatik üretildi
}

// DefaultKeyFile RHEL kurulumunda kur/guncelle betiklerinin yazdığı yer.
const DefaultKeyFile = "/etc/appmon/master.key"

// Config anahtarın nereden alınacağını belirler.
type Config struct {
	Env     string // APM_MASTER_KEY
	File    string // APM_MASTER_KEY_FILE
	DataDir string // APM_DATA_DIR: dosya yoksa burada üretilir ("" = üretme)
	// Test için: varsayılan dosya yolunu değiştirir.
	DefaultFile string
}

// ErrDisabled ana anahtar bulunamadığında ve üretilemediğinde döner.
var ErrDisabled = errors.New("ana anahtar bulunamadı ve üretilemedi")

// Load yapılandırmaya göre anahtarı bulur veya üretir.
func Load(c Config) (*Vault, error) {
	if c.DefaultFile == "" {
		c.DefaultFile = DefaultKeyFile
	}
	if strings.TrimSpace(c.Env) != "" {
		k, err := parseKey(c.Env)
		if err != nil {
			return nil, fmt.Errorf("APM_MASTER_KEY: %w", err)
		}
		return build(k, "env", ""), nil
	}
	for _, p := range []string{c.File, c.DefaultFile} {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			if p == c.File { // açıkça verilen dosya yoksa hata; varsayılan yoksa sonraki adıma geç
				return nil, fmt.Errorf("APM_MASTER_KEY_FILE bulunamadı: %s", p)
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("ana anahtar okunamadı (%s): %w", p, err)
		}
		k, err := parseKey(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		return build(k, "file", p), nil
	}
	if c.DataDir == "" {
		return nil, ErrDisabled
	}
	path := filepath.Join(c.DataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := parseKey(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return build(k, "file", path), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDisabled, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDisabled, err)
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return nil, fmt.Errorf("%w: %v", ErrDisabled, werr)
	}
	v := build(raw, "generated", path)
	v.Generated = true
	return v, nil
}

// New doğrudan 32 baytlık anahtardan Vault üretir (test için).
func New(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("anahtar 32 bayt olmalı")
	}
	return build(key, "env", ""), nil
}

// GenerateKey yeni bir ana anahtar (base64) üretir.
func GenerateKey() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return base64.StdEncoding.EncodeToString(raw)
}

func parseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("ana anahtar 32 baytlık base64 veya 64 karakterlik hex olmalı (openssl rand -base64 32)")
}

func build(key []byte, source, path string) *Vault {
	blk, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(blk)
	id := sha256.Sum256(append([]byte("appmon-vault-key-id:"), key...))
	return &Vault{aead: g, KeyID: hex.EncodeToString(id[:6]), Source: source, Path: path}
}

// Seal düz metni şifreler; çıktı nonce||şifreli metin. aad değere bağlanır (başka kayda taşınamaz).
func (v *Vault) Seal(plain []byte, aad string) []byte {
	nonce := make([]byte, v.aead.NonceSize())
	rand.Read(nonce)
	return v.aead.Seal(nonce, nonce, plain, []byte(aad))
}

// Open Seal çıktısını çözer.
func (v *Vault) Open(blob []byte, aad string) ([]byte, error) {
	n := v.aead.NonceSize()
	if len(blob) < n+v.aead.Overhead() {
		return nil, errors.New("şifreli veri bozuk")
	}
	p, err := v.aead.Open(nil, blob[:n], blob[n:], []byte(aad))
	if err != nil {
		return nil, errors.New("çözülemedi: ana anahtar bu kayıtla eşleşmiyor veya veri bozulmuş")
	}
	return p, nil
}
