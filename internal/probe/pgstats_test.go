package probe

import (
	"context"
	"os"
	"testing"
	"time"
)

// APM_TEST_PG ayarlıysa gerçek bir PostgreSQL'e karşı çalışır.
func TestPGStats(t *testing.T) {
	dsn := os.Getenv("APM_TEST_PG")
	if dsn == "" {
		t.Skip("APM_TEST_PG tanımlı değil")
	}
	r := RunX(context.Background(), Spec{Type: "pgstats", Target: dsn, Timeout: 8 * time.Second}, false)
	if r.Err != nil || !r.Up {
		t.Fatalf("%+v", r)
	}
	for _, k := range []string{"db.connections", "db.connections_pct", "db.cache_hit_pct", "db.size_mb", "db.longest_query_s", "db.waiting_locks"} {
		if _, ok := r.Extra[k]; !ok {
			t.Errorf("%s yok: %v", k, r.Extra)
		}
	}
	if r.Extra["db.connections"] < 1 || r.Extra["db.size_mb"] <= 0 {
		t.Errorf("değerler mantıksız: %v", r.Extra)
	}
}

// pg_stat_statements yoksa probe başarısız sayılmaz, neden Note'ta döner; varsa sorgular okunur.
func TestPGStatements(t *testing.T) {
	dsn := os.Getenv("APM_TEST_PG")
	if dsn == "" {
		t.Skip("APM_TEST_PG tanımlı değil")
	}
	snap := &StmtSnapshot{}
	r := RunX(context.Background(), Spec{Type: "pgstats", Target: dsn, Timeout: 8 * time.Second, StmtOut: snap}, false)
	if r.Err != nil || !r.Up {
		t.Fatalf("%+v", r)
	}
	if !snap.Available {
		if snap.Note == "" {
			t.Fatalf("uzantı yokken neden yazılmalı")
		}
		t.Logf("pg_stat_statements kullanılamıyor: %s", snap.Note)
		return
	}
	if _, ok := r.Extra["db.slow_queries"]; !ok {
		t.Fatalf("db.slow_queries yok: %v", r.Extra)
	}
	for _, s := range snap.Stmts {
		if s.QueryID == "" || s.Calls <= 0 {
			t.Fatalf("satır geçersiz: %+v", s)
		}
	}
}
