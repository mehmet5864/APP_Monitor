package probe

import (
	"crypto/x509"
	"errors"
	"os"
	"strings"
)

// ExtraRoots kurum içi (özel) sertifika yetkilerini de içeren güven havuzudur. nil ise sistem havuzu kullanılır.
var ExtraRoots *x509.CertPool

// LoadCAFile sistem güven havuzuna PEM dosyasındaki ek sertifika yetkilerini ekler (APM_CA_FILE).
func LoadCAFile(path string) (int, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	before := len(pool.Subjects()) //nolint:staticcheck
	if !pool.AppendCertsFromPEM(pem) {
		return 0, errors.New("dosyada geçerli PEM sertifika bulunamadı")
	}
	ExtraRoots = pool
	return len(pool.Subjects()) - before, nil //nolint:staticcheck
}

// tlsHint güvenilmeyen sertifika yetkisi hatasına çözüm yolunu ekler.
func tlsHint(err error) error {
	if err == nil {
		return nil
	}
	m := err.Error()
	if strings.Contains(m, "unknown authority") || strings.Contains(m, "x509:") {
		return errors.New(m + " — sertifika yetkisi güvenilmiyor: kurum CA dosyasını APM_CA_FILE ile tanıtın veya probe'ta \"Sertifika zincirini doğrula\" seçeneğini kapatın")
	}
	return err
}
