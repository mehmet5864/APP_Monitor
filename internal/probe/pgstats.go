package probe

import (
	"context"
	"database/sql"
)

// pgStats PostgreSQL sağlık metriklerini toplar (yalnızca okuma sorguları; pg_monitor/varsayılan kullanıcı yeterli).
// Üretilen metrikler: db.connections, db.connections_pct, db.cache_hit_pct, db.deadlocks, db.size_mb,
// db.longest_query_s, db.idle_in_tx_s, db.waiting_locks, db.repl_lag_s (yedek sunucuda), db.xact_commit/rollback (toplam).
func pgStats(ctx context.Context, db *sql.DB) (map[string]float64, error) {
	out := map[string]float64{}
	var v sql.NullFloat64
	one := func(name, q string) error {
		v = sql.NullFloat64{}
		if err := db.QueryRowContext(ctx, q).Scan(&v); err != nil {
			return err
		}
		if v.Valid {
			out[name] = v.Float64
		}
		return nil
	}
	if err := one("db.connections", `SELECT count(*)::float8 FROM pg_stat_activity WHERE datname IS NOT NULL`); err != nil {
		return nil, err
	}
	if err := one("db.connections_pct", `SELECT 100.0 * (SELECT count(*) FROM pg_stat_activity WHERE datname IS NOT NULL) / NULLIF(current_setting('max_connections')::float8, 0)`); err != nil {
		return nil, err
	}
	// Aşağıdakiler yetki/sürüm farkı yüzünden başarısız olabilir; bu durumda ilgili metrik atlanır, probe başarısız sayılmaz.
	one("db.cache_hit_pct", `SELECT 100.0 * blks_hit / NULLIF(blks_hit + blks_read, 0) FROM pg_stat_database WHERE datname = current_database()`)
	one("db.deadlocks", `SELECT deadlocks::float8 FROM pg_stat_database WHERE datname = current_database()`)
	one("db.xact_commit", `SELECT xact_commit::float8 FROM pg_stat_database WHERE datname = current_database()`)
	one("db.xact_rollback", `SELECT xact_rollback::float8 FROM pg_stat_database WHERE datname = current_database()`)
	one("db.size_mb", `SELECT pg_database_size(current_database()) / 1048576.0`)
	one("db.longest_query_s", `SELECT COALESCE(max(extract(epoch FROM now() - query_start)), 0) FROM pg_stat_activity
		WHERE state = 'active' AND pid <> pg_backend_pid() AND datname IS NOT NULL`)
	one("db.idle_in_tx_s", `SELECT COALESCE(max(extract(epoch FROM now() - xact_start)), 0) FROM pg_stat_activity
		WHERE state LIKE 'idle in transaction%' AND datname IS NOT NULL`)
	one("db.waiting_locks", `SELECT count(*)::float8 FROM pg_locks WHERE NOT granted`)
	one("db.repl_lag_s", `SELECT CASE WHEN pg_is_in_recovery() THEN COALESCE(extract(epoch FROM now() - pg_last_xact_replay_timestamp()), 0) END`)
	return out, nil
}
