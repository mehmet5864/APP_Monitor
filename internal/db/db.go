// Package db PostgreSQL bağlantısını açar ve gömülü migration dosyalarını sırayla uygular.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"sort"
	"time"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open veritabanına bağlanır; Docker'da PostgreSQL geç ayağa kalkabileceği için yeniden dener.
func Open(dsn string) (*sql.DB, error) {
	d, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(20)
	d.SetMaxIdleConns(5)
	d.SetConnMaxLifetime(30 * time.Minute)
	var last error
	for i := 1; i <= 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		last = d.PingContext(ctx)
		cancel()
		if last == nil {
			return d, nil
		}
		log.Printf("veritabanı hazır değil (%d/30): %v", i, last)
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("veritabanına bağlanılamadı: %w", last)
}

// Migrate henüz uygulanmamış migration dosyalarını ada göre sıralı uygular.
func Migrate(d *sql.DB) error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := d.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		log.Printf("migration uygulandı: %s", name)
	}
	return nil
}
