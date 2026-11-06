// Package update arayüzden yüklenen güncelleme paketlerini (tar.gz) güvenli biçimde doğrular ve hazırlar.
// Paketi uygulayan (ikili dosyaları değiştiren) kısım ayrıcalıklı bir systemd hizmetidir; bu paket yalnızca hazırlık yapar.
package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxPackage = 400 << 20 // açılmış toplam boyut sınırı
	maxFile    = 200 << 20
)

var (
	verRe  = regexp.MustCompile(`^v(\d{1,4})\.(\d{1,4})\.(\d{1,4})$`)
	binRe  = regexp.MustCompile(`^bin/[A-Za-z0-9._-]{1,80}$`)
	idRe   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	archRe = regexp.MustCompile(`^appmon-server-linux-([a-z0-9]+)$`)
)

// Manifest paketin içeriğini ve sağlama toplamlarını tanımlar.
type Manifest struct {
	Format  int               `json:"format"`
	Product string            `json:"product"`
	Version string            `json:"version"`
	Created string            `json:"created,omitempty"`
	Notes   string            `json:"notes,omitempty"`
	Files   map[string]string `json:"files"` // yol -> sha256 (hex)
}

// Staged yüklenmiş ve doğrulanmış (henüz uygulanmamış) paketi anlatır.
type Staged struct {
	ID      string    `json:"id"`
	Version string    `json:"version"`
	Created string    `json:"created,omitempty"`
	Notes   string    `json:"notes,omitempty"`
	Signed  bool      `json:"signed"`
	Files   []string  `json:"files"`
	Staged  time.Time `json:"staged_at"`
}

// ParseVersion "v1.2.3" biçimini sayılara çevirir.
func ParseVersion(v string) ([3]int, error) {
	m := verRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{}, fmt.Errorf("geçersiz sürüm %q (vANA.ARA.YAMA olmalı)", v)
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, nil
}

// Compare a<b için -1, eşitse 0, a>b için 1 döner.
func Compare(a, b string) (int, error) {
	x, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	y, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

// LoadPublicKey PEM (PKIX) Ed25519 genel anahtarını okur.
func LoadPublicKey(file string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("PEM bulunamadı")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	pk, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("anahtar Ed25519 değil")
	}
	return pk, nil
}

// limitedCopy en çok n bayt kopyalar; fazlası hata.
func limitedCopy(dst io.Writer, src io.Reader, n int64) (int64, error) {
	w, err := io.Copy(dst, io.LimitReader(src, n+1))
	if err == nil && w > n {
		err = errors.New("paket içindeki dosya çok büyük")
	}
	return w, err
}

// Stage paketi dir altına açar ve doğrular. pub nil değilse imza zorunludur.
// arch çalışan sunucunun mimarisidir (ör. amd64); current çalışan sürümdür.
func Stage(r io.Reader, dir, arch string, pub ed25519.PublicKey, current string) (*Staged, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(dir, ".tmp-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, errors.New("dosya geçerli bir .tar.gz değil")
	}
	tr := tar.NewReader(gz)
	var total int64
	extracted := map[string]bool{}
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("paket okunamadı (bozuk veya eksik aktarılmış olabilir)")
		}
		if hd.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hd.Name, "./"))
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			return nil, errors.New("pakette güvenli olmayan yol var")
		}
		parts := strings.Split(name, "/")
		if len(parts) >= 2 && parts[0] != "bin" { // appmon-guncelleme/ gibi üst klasörü at
			name = strings.Join(parts[1:], "/")
		}
		if name != "manifest.json" && name != "manifest.sig" && !binRe.MatchString(name) {
			continue // diğer dosyalar (kılavuz, betikler) yok sayılır; ayrıcalıklı tarafta çalıştırılmaz
		}
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, errors.New("pakette yinelenen dosya var: " + name)
		}
		w, err := limitedCopy(f, tr, maxFile)
		f.Close()
		if err != nil {
			return nil, err
		}
		if total += w; total > maxPackage {
			return nil, errors.New("paket çok büyük")
		}
		extracted[name] = true
	}
	if !extracted["manifest.json"] {
		return nil, errors.New("pakette manifest.json yok: bu, arayüzden yüklenebilen bir güncelleme paketi değil (yeni paketleme betiğiyle üretilmeli)")
	}
	mb, err := os.ReadFile(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, errors.New("manifest.json okunamadı")
	}
	if m.Format != 1 || m.Product != "appmon" {
		return nil, errors.New("paket APP_MON güncelleme paketi değil veya biçimi desteklenmiyor")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return nil, err
	}
	signed := false
	if pub != nil {
		sig, err := os.ReadFile(filepath.Join(tmp, "manifest.sig"))
		if err != nil {
			return nil, errors.New("paket imzalı değil (manifest.sig yok); bu kurulum yalnızca imzalı paketleri kabul eder")
		}
		if !ed25519.Verify(pub, mb, sig) {
			return nil, errors.New("paket imzası geçersiz: paket değiştirilmiş veya farklı bir anahtarla imzalanmış")
		}
		signed = true
	}
	var names []string
	for p, sum := range m.Files {
		if !binRe.MatchString(p) {
			return nil, errors.New("manifestte geçersiz dosya yolu: " + p)
		}
		if !extracted[p] {
			return nil, errors.New("manifestte listelenen dosya pakette yok: " + p)
		}
		if _, err := hex.DecodeString(sum); err != nil || len(sum) != 64 {
			return nil, errors.New("manifestte geçersiz sağlama toplamı: " + p)
		}
		got, err := fileSum(filepath.Join(tmp, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		if got != sum {
			return nil, errors.New("sağlama toplamı uyuşmuyor (paket bozuk): " + p)
		}
		names = append(names, p)
	}
	for p := range extracted {
		if p != "manifest.json" && p != "manifest.sig" && m.Files[p] == "" {
			return nil, errors.New("pakette manifestte olmayan dosya var: " + p)
		}
	}
	sort.Strings(names)
	srv := "bin/appmon-server-linux-" + arch
	if m.Files[srv] == "" {
		return nil, fmt.Errorf("pakette bu sunucunun mimarisi (%s) için ikili dosya yok", arch)
	}
	if err := checkELF(filepath.Join(tmp, filepath.FromSlash(srv))); err != nil {
		return nil, err
	}
	if current != "" {
		if c, err := Compare(m.Version, current); err == nil && c < 0 {
			return nil, fmt.Errorf("paket sürümü (%s) çalışan sürümden (%s) eski; veritabanı değişiklikleri geri alınamadığı için eski sürüme geçilemez", m.Version, current)
		}
	}
	// Ayrıcalıklı güncelleyicinin kullanacağı sağlama listesi ve sürüm dosyası (doğrulanmış manifestten üretilir).
	var sums strings.Builder
	for _, p := range names {
		sums.WriteString(m.Files[p] + "  " + p + "\n")
	}
	if err := os.WriteFile(filepath.Join(tmp, "SHA256SUMS"), []byte(sums.String()), 0o640); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "SURUM"), []byte(m.Version+"\n"), 0o640); err != nil {
		return nil, err
	}
	for _, p := range names {
		os.Chmod(filepath.Join(tmp, filepath.FromSlash(p)), 0o755)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	clearStaged(dir, "staged-") // önceki hazırlanmış paketi sil
	if err := os.Rename(tmp, filepath.Join(dir, "staged-"+id)); err != nil {
		return nil, err
	}
	ok = true
	return &Staged{ID: id, Version: m.Version, Created: m.Created, Notes: m.Notes, Signed: signed, Files: names, Staged: time.Now()}, nil
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkELF(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	b := make([]byte, 4)
	if _, err := io.ReadFull(f, b); err != nil || string(b) != "\x7fELF" {
		return errors.New("sunucu ikili dosyası geçerli bir Linux programı değil")
	}
	return nil
}

// IDValid hazırlanmış paket kimliğinin biçimini denetler.
func IDValid(id string) bool { return idRe.MatchString(id) }

// Load hazırlanmış paketi okur; yoksa (nil, nil) döner.
func Load(dir string) (*Staged, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "staged-") || !idRe.MatchString(strings.TrimPrefix(e.Name(), "staged-")) {
			continue
		}
		id := strings.TrimPrefix(e.Name(), "staged-")
		mb, err := os.ReadFile(filepath.Join(dir, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(mb, &m) != nil {
			continue
		}
		info, _ := e.Info()
		var names []string
		for p := range m.Files {
			names = append(names, p)
		}
		sort.Strings(names)
		_, sigErr := os.Stat(filepath.Join(dir, e.Name(), "manifest.sig"))
		st := &Staged{ID: id, Version: m.Version, Created: m.Created, Notes: m.Notes, Signed: sigErr == nil, Files: names}
		if info != nil {
			st.Staged = info.ModTime()
		}
		return st, nil
	}
	return nil, nil
}

func clearStaged(dir string, prefixes ...string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		for _, p := range prefixes {
			if e.IsDir() && strings.HasPrefix(e.Name(), p) {
				os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// Clear hazırlanmış paketleri ve yarım kalmış geçici klasörleri siler.
func Clear(dir string) { clearStaged(dir, "staged-", ".tmp-") }

// ApplyRequest ayrıcalıklı güncelleyiciyi tetikleyen istektir.
type ApplyRequest struct {
	ID   string    `json:"id"`
	By   string    `json:"by"`
	Time time.Time `json:"time"`
}

// RequestApply apply.json dosyasını (atomik olarak) yazar; systemd yol birimi bunu görünce güncelleyiciyi çalıştırır.
func RequestApply(dir, id, by string) error {
	if !IDValid(id) {
		return errors.New("geçersiz paket kimliği")
	}
	if _, err := os.Stat(filepath.Join(dir, "staged-"+id, "manifest.json")); err != nil {
		return errors.New("hazırlanmış paket bulunamadı; paketi yeniden yükleyin")
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.json")); err == nil {
		return errors.New("zaten bir güncelleme isteği işleniyor")
	}
	b, _ := json.Marshal(ApplyRequest{ID: id, By: by, Time: time.Now()})
	tmp := filepath.Join(dir, ".apply.tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "apply.json"))
}

// Status güncelleyicinin son sonucudur (status.json).
type Status struct {
	State   string `json:"state"` // running, ok, failed, rolled_back
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Message string `json:"message,omitempty"`
	Time    string `json:"time,omitempty"`
}

func ReadStatus(dir string) *Status {
	b, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return nil
	}
	var s Status
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// Pending apply.json'ın (işlenmeyi beklediği) varlığını söyler.
func Pending(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "apply.json"))
	return err == nil
}

// Ready güncelleyici kurulu mu (kurulum betiği READY dosyasını bırakır).
func Ready(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "READY"))
	return err == nil
}
