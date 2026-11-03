package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

var (
	ErrLicenseLimit   = errors.New("lisanstaki asset limitine ulaşıldı; yeni asset eklenemez")
	ErrLicenseExpired = errors.New("lisans süresi dolmuş")
)

func normalizeLanguage(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dotnet", ".net", "csharp", "c#", "dotnet-core":
		return "dotnet"
	case "java":
		return "java"
	case "python":
		return "python"
	case "go", "golang":
		return "go"
	}
	return "none"
}

// ensureAsset asset'i bulur; yoksa lisans limitini kontrol ederek oluşturur.
// Lisans satırı kilitlenir, böylece eşzamanlı kayıtlar limiti aşamaz.
func (s *Server) ensureAsset(ctx context.Context, tx *sql.Tx, tenant int64, name, kind, language, collection string) (id int64, created bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT id FROM assets WHERE tenant_id = $1 AND name = $2`, tenant, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}

	var limit int
	var validUntil sql.NullTime
	if err = tx.QueryRowContext(ctx,
		`SELECT asset_limit, valid_until FROM licenses WHERE tenant_id = $1 FOR UPDATE`, tenant).Scan(&limit, &validUntil); err != nil {
		return 0, false, err
	}
	// Kilidi aldıktan sonra başka bir istek aynı asset'i eklemiş olabilir.
	err = tx.QueryRowContext(ctx, `SELECT id FROM assets WHERE tenant_id = $1 AND name = $2`, tenant, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if validUntil.Valid && validUntil.Time.Before(time.Now()) {
		return 0, false, ErrLicenseExpired
	}
	var used int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM assets WHERE tenant_id = $1`, tenant).Scan(&used); err != nil {
		return 0, false, err
	}
	if used >= limit {
		return 0, false, ErrLicenseLimit
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO assets (tenant_id, name, kind, language, collection) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		tenant, name, kind, language, collection).Scan(&id)
	return id, err == nil, err
}

func licenseStatus(err error) (int, bool) {
	if errors.Is(err, ErrLicenseLimit) || errors.Is(err, ErrLicenseExpired) {
		return http.StatusPaymentRequired, true
	}
	return 0, false
}
