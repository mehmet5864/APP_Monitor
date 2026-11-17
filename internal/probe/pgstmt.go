package probe

import (
	"context"
	"database/sql"
	"strings"
)

// Stmt pg_stat_statements'ten okunan tek bir (parametresizleştirilmiş) sorgunun birikimli istatistiğidir.
type Stmt struct {
	QueryID string
	Query   string
	Calls   int64
	TotalMS float64
	MeanMS  float64
	MaxMS   float64
	Rows    int64
}

// StmtSnapshot bir pgstats probe turunda okunan yavaş sorgu özetidir.
type StmtSnapshot struct {
	Available bool   // uzantı kullanılabildi mi
	Note      string // kullanılamıyorsa nedeni
	Stmts     []Stmt
}

// SlowQueryMS "yavaş sorgu" sayılan ortalama süredir (ms); db.slow_queries metriği bunu kullanır.
const SlowQueryMS = 500.0

// collectStatements en çok süre harcayan ve ortalaması en yüksek sorguları okur. Uzantı yoksa veya yetki yoksa
// probe başarısız sayılmaz; neden Note alanında döner. Sorgu metinleri pg_stat_statements tarafından zaten
// parametresizleştirilmiştir (sabit değerler $1, $2 olarak görünür).
func collectStatements(ctx context.Context, db *sql.DB, out map[string]float64) *StmtSnapshot {
	snap := &StmtSnapshot{}
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
				snap.Note = "pg_stat_statements uzantısı bu veritabanında kurulu değil (shared_preload_libraries = 'pg_stat_statements' ve CREATE EXTENSION pg_stat_statements)"
			case strings.Contains(msg, "permission denied"):
				snap.Note = "pg_stat_statements okuma izni yok (izleme kullanıcısına pg_monitor rolünü verin)"
			case strings.Contains(msg, "column") && cols[0] == "total_exec_time":
				continue // PostgreSQL 12 ve öncesi: eski sütun adlarıyla yeniden dene
			case strings.Contains(msg, "must be loaded via shared_preload_libraries"):
				snap.Note = "pg_stat_statements yüklenmemiş (shared_preload_libraries ayarı ve yeniden başlatma gerekir)"
			default:
				snap.Note = "sorgu istatistikleri okunamadı"
			}
			return snap
		}
		for rows.Next() {
			var s Stmt
			if rows.Scan(&s.QueryID, &s.Query, &s.Calls, &s.TotalMS, &s.MeanMS, &s.MaxMS, &s.Rows) == nil {
				snap.Stmts = append(snap.Stmts, s)
			}
		}
		rows.Close()
		snap.Available = true
		break
	}
	if snap.Available {
		slow, top := 0.0, 0.0
		for _, s := range snap.Stmts {
			if s.MeanMS >= SlowQueryMS && s.Calls >= 2 {
				slow++
			}
			if s.MeanMS > top {
				top = s.MeanMS
			}
		}
		out["db.slow_queries"] = slow
		out["db.top_query_mean_ms"] = top
	}
	return snap
}
