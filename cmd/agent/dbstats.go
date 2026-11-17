package main

import (
	"context"
	"database/sql"
	"log"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Veritabanı sorgu istatistikleri (agent 1.4.0): ajan, makinedeki PostgreSQL'in pg_stat_statements görünümünü okur ve
// en çok süre harcayan sorguları Performans › Veritabanı sorguları sayfasına gönderir. Bağlantı bilgisi yalnızca ajanın
// yapılandırmasındadır (APM_PG_DSN, "postgres://kullanici:parola@127.0.0.1:5432/veritabani?sslmode=disable");
// sunucuya parola gitmez. Yalnızca SELECT çalıştırılır; izleme kullanıcısına pg_monitor rolü yeterlidir.

type dbStmt struct {
	ID    string  `json:"id"`
	Q     string  `json:"q"`
	Calls int64   `json:"calls"`
	Total float64 `json:"total_ms"`
	Mean  float64 `json:"mean_ms"`
	Max   float64 `json:"max_ms"`
	Rows  int64   `json:"rows"`
}

type dbReport struct {
	Engine    string   `json:"engine"`
	DB        string   `json:"db"`
	Available bool     `json:"available"`
	Note      string   `json:"note,omitempty"`
	Stmts     []dbStmt `json:"stmts,omitempty"`
}

// withTimeout DSN'e bağlantı zaman aşımı ekler (yoksa).
func withTimeout(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			q := u.Query()
			if q.Get("connect_timeout") == "" {
				q.Set("connect_timeout", "5")
			}
			u.RawQuery = q.Encode()
			return u.String()
		}
		return dsn
	}
	if !strings.Contains(dsn, "connect_timeout") {
		dsn += " connect_timeout=5"
	}
	return dsn
}

func splitDSNs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";;") {
		if p = strings.TrimSpace(p); p != "" && len(out) < 8 {
			out = append(out, p)
		}
	}
	return out
}

func collectDBStats(dsns []string) []dbReport {
	var out []dbReport
	for _, dsn := range dsns {
		out = append(out, collectPG(dsn))
	}
	return out
}

func collectPG(dsn string) dbReport {
	rep := dbReport{Engine: "postgres"}
	db, err := sql.Open("postgres", withTimeout(dsn))
	if err != nil {
		rep.Note = "bağlantı ayarı geçersiz"
		return rep
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&rep.DB); err != nil {
		rep.Note = "veritabanına bağlanılamadı (APM_PG_DSN, ağ ve parola kontrol edin)"
		log.Printf("dbstats: bağlanılamadı: %v", sanitizeErr(err, dsn))
		return rep
	}
	for _, cols := range [][3]string{{"total_exec_time", "mean_exec_time", "max_exec_time"}, {"total_time", "mean_time", "max_time"}} {
		q := `SELECT queryid::text, left(regexp_replace(query, '\s+', ' ', 'g'), 1000), calls, ` + cols[0] + `, ` + cols[1] + `, ` + cols[2] + `, rows
		      FROM pg_stat_statements
		      WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND calls > 0
		        AND query NOT ILIKE '%pg_stat_statements%' AND queryid IS NOT NULL
		      ORDER BY ` + cols[0] + ` DESC LIMIT 60`
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			msg := err.Error()
			switch {
			case strings.Contains(msg, "does not exist") && strings.Contains(msg, "pg_stat_statements"):
				rep.Note = "pg_stat_statements uzantısı bu veritabanında kurulu değil (shared_preload_libraries = 'pg_stat_statements' ve CREATE EXTENSION pg_stat_statements)"
			case strings.Contains(msg, "permission denied"):
				rep.Note = "pg_stat_statements okuma izni yok (izleme kullanıcısına pg_monitor rolünü verin)"
			case strings.Contains(msg, "column") && cols[0] == "total_exec_time":
				continue // PostgreSQL 12 ve öncesi
			case strings.Contains(msg, "must be loaded via shared_preload_libraries"):
				rep.Note = "pg_stat_statements yüklenmemiş (shared_preload_libraries ayarı ve yeniden başlatma gerekir)"
			default:
				rep.Note = "sorgu istatistikleri okunamadı"
			}
			return rep
		}
		for rows.Next() {
			var s dbStmt
			if rows.Scan(&s.ID, &s.Q, &s.Calls, &s.Total, &s.Mean, &s.Max, &s.Rows) == nil {
				rep.Stmts = append(rep.Stmts, s)
			}
		}
		rows.Close()
		rep.Available = true
		break
	}
	return rep
}

// sanitizeErr hata metninden bağlantı dizesini (parola dahil) ayıklar.
func sanitizeErr(err error, dsn string) string {
	m := err.Error()
	if dsn != "" {
		m = strings.ReplaceAll(m, dsn, "<dsn>")
	}
	if u, e := url.Parse(dsn); e == nil && u.User != nil {
		if p, ok := u.User.Password(); ok && p != "" {
			m = strings.ReplaceAll(m, p, "***")
		}
	}
	return m
}
