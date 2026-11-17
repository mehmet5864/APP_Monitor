package app

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Ajanın gönderdiği veritabanı sorgu istatistikleri (ajan 1.4.0): Performans › Veritabanı sorguları sayfasında
// sunucu probe'larının topladığı pg_stat_statements verisiyle birlikte listelenir.

type AgentDBStmt struct {
	ID    string  `json:"id"`
	Q     string  `json:"q"`
	Calls int64   `json:"calls"`
	Total float64 `json:"total_ms"`
	Mean  float64 `json:"mean_ms"`
	Max   float64 `json:"max_ms"`
	Rows  int64   `json:"rows"`
}

type AgentDB struct {
	Engine    string        `json:"engine"`
	DB        string        `json:"db"`
	Available bool          `json:"available"`
	Note      string        `json:"note"`
	Stmts     []AgentDBStmt `json:"stmts"`
}

const maxDBStmts, maxDBs = 100, 8

func okNum(v float64) bool { return v >= 0 && v < 1e15 }

// storeDBStats her veritabanı için özeti günceller; bu turda görünmeyen eski satırlar silinir.
func storeDBStats(ctx context.Context, tx *sql.Tx, tenant, asset int64, list []AgentDB) error {
	if len(list) > maxDBs {
		list = list[:maxDBs]
	}
	ts := time.Now()
	for _, d := range list {
		name := strings.TrimSpace(d.DB)
		if d.Engine != "postgres" || name == "" || len(name) > 100 {
			continue
		}
		note := d.Note
		if len(note) > 300 {
			note = note[:300]
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_db_state (asset_id, tenant_id, db, available, note, updated_at) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (asset_id, db) DO UPDATE SET available = EXCLUDED.available, note = EXCLUDED.note, updated_at = EXCLUDED.updated_at`,
			asset, tenant, name, d.Available, note, ts); err != nil {
			return err
		}
		if !d.Available {
			continue
		}
		n := 0
		for _, st := range d.Stmts {
			if n >= maxDBStmts {
				break
			}
			q := strings.TrimSpace(st.Q)
			if st.ID == "" || len(st.ID) > 40 || q == "" || st.Calls < 0 || !okNum(st.Total) || !okNum(st.Mean) || !okNum(st.Max) || st.Rows < 0 {
				continue
			}
			if len(q) > 1000 {
				q = q[:1000]
			}
			n++
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_db_statements (asset_id, tenant_id, db, queryid, query, calls, total_ms, mean_ms, max_ms, rows_total, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
				ON CONFLICT (asset_id, db, queryid) DO UPDATE SET
				  prev_calls = agent_db_statements.calls, prev_total = agent_db_statements.total_ms, prev_at = agent_db_statements.updated_at,
				  query = EXCLUDED.query, calls = EXCLUDED.calls, total_ms = EXCLUDED.total_ms, mean_ms = EXCLUDED.mean_ms,
				  max_ms = EXCLUDED.max_ms, rows_total = EXCLUDED.rows_total, updated_at = EXCLUDED.updated_at`,
				asset, tenant, name, st.ID, q, st.Calls, st.Total, st.Mean, st.Max, st.Rows, ts); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_db_statements WHERE asset_id = $1 AND db = $2 AND updated_at < $3`, asset, name, ts); err != nil {
			return err
		}
	}
	return nil
}
