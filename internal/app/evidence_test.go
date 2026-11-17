package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

func TestAuditHash(t *testing.T) {
	a := auditHash("", 1, 1, "ali", "x", "d", 100)
	if a != auditHash("", 1, 1, "ali", "x", "d", 100) {
		t.Fatal("hash deterministik olmalı")
	}
	// Alan kaydırma: "ab"+"c" ile "a"+"bc" aynı hash'i vermemeli.
	if auditHash("", 1, 1, "ab", "c", "", 1) == auditHash("", 1, 1, "a", "bc", "", 1) {
		t.Fatal("alan sınırları hash'e yansımalı")
	}
	for _, v := range []string{auditHash("p", 1, 1, "ali", "x", "d", 100), auditHash("", 2, 1, "ali", "x", "d", 100), auditHash("", 1, 2, "ali", "x", "d", 100),
		auditHash("", 1, 1, "veli", "x", "d", 100), auditHash("", 1, 1, "ali", "y", "d", 100), auditHash("", 1, 1, "ali", "x", "e", 100), auditHash("", 1, 1, "ali", "x", "d", 101)} {
		if v == a {
			t.Fatal("her alan hash'i etkilemeli")
		}
	}
}

func TestEvidenceChain(t *testing.T) {
	dsn := os.Getenv("APM_TEST_DSN")
	if dsn == "" {
		t.Skip("APM_TEST_DSN yok")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Exec(`TRUNCATE tenants RESTART IDENTITY CASCADE`)
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	d.Exec(`DELETE FROM evidence_keys`)
	v, _ := vault.New(bytes.Repeat([]byte{7}, 32))
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 4}
	tid, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	call := func(method, path string, body any) (int, []byte) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	chain := func() ChainStatus {
		c, b := call("GET", "/api/evidence/chain", nil)
		if c != 200 {
			t.Fatalf("chain: %d %s", c, b)
		}
		var st ChainStatus
		json.Unmarshal(b, &st)
		return st
	}
	ctx := t.Context()
	// Eşzamanlı yazımlar zinciri bozmamalı.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.audit(ctx, tid, "ali", "test.action", "kayıt "+itoa(i))
		}(i)
	}
	wg.Wait()
	st := chain()
	if !st.OK || st.Checked < 20 || st.HeadHash == "" {
		t.Fatalf("zincir sağlam olmalı: %+v", st)
	}
	// Kayıt içeriği değiştirilirse yakalanır.
	var victim int64
	d.QueryRow(`SELECT id FROM audit_log WHERE tenant_id = $1 AND detail = 'kayıt 5'`, tid).Scan(&victim)
	d.Exec(`UPDATE audit_log SET detail = 'başka' WHERE id = $1`, victim)
	if st := chain(); st.OK || st.BrokenID != victim || !strings.Contains(st.Reason, "değiştirilmiş") {
		t.Fatalf("değiştirilen kayıt yakalanmalı: %+v", st)
	}
	d.Exec(`UPDATE audit_log SET detail = 'kayıt 5' WHERE id = $1`, victim)
	if !chain().OK {
		t.Fatal("geri alınınca zincir yeniden sağlam olmalı")
	}
	// Ortadan kayıt silinirse yakalanır.
	var mid int64
	d.QueryRow(`SELECT id FROM audit_log WHERE tenant_id = $1 AND hash IS NOT NULL ORDER BY id OFFSET 10 LIMIT 1`, tid).Scan(&mid)
	var saved struct {
		actor, action, detail, ph, h string
		at                           time.Time
	}
	d.QueryRow(`SELECT actor, action, detail, at, prev_hash, hash FROM audit_log WHERE id = $1`, mid).Scan(&saved.actor, &saved.action, &saved.detail, &saved.at, &saved.ph, &saved.h)
	d.Exec(`DELETE FROM audit_log WHERE id = $1`, mid)
	if st := chain(); st.OK || !strings.Contains(st.Reason, "bağlantı kopuk") {
		t.Fatalf("silinen kayıt yakalanmalı: %+v", st)
	}
	d.Exec(`INSERT INTO audit_log (id, tenant_id, actor, action, detail, at, prev_hash, hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, mid, tid, saved.actor, saved.action, saved.detail, saved.at, saved.ph, saved.h)
	if !chain().OK {
		t.Fatal("kayıt geri konunca zincir sağlam olmalı")
	}

	// Rapor üret, indir, doğrula.
	period := time.Now().Format("2006-01")
	c, b := call("POST", "/api/evidence/reports", map[string]string{"period": period})
	if c != 201 {
		t.Fatalf("rapor: %d %s", c, b)
	}
	var created struct {
		ID      int  `json:"id"`
		ChainOK bool `json:"chain_ok"`
	}
	json.Unmarshal(b, &created)
	if !created.ChainOK {
		t.Fatal("rapor sağlam zinciri kaydetmeli")
	}
	if c, _ := call("POST", "/api/evidence/reports", map[string]string{"period": "2999-01"}); c != 400 {
		t.Fatalf("gelecek dönem reddedilmeli: %d", c)
	}
	if c, _ := call("POST", "/api/evidence/reports", map[string]string{"period": "abc"}); c != 400 {
		t.Fatalf("geçersiz dönem reddedilmeli: %d", c)
	}
	c, pkgBytes := call("GET", "/api/evidence/reports/"+itoa(created.ID)+"/package", nil)
	if c != 200 {
		t.Fatalf("paket: %d", c)
	}
	verify := func(pkg []byte) map[string]any {
		c, b := call("POST", "/api/evidence/verify", json.RawMessage(pkg))
		if c != 200 {
			t.Fatalf("verify: %d %s", c, b)
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	m := verify(pkgBytes)
	if m["signature_valid"] != true || m["chain_match"] != true || m["key_is_this_server"] != true {
		t.Fatalf("sağlam paket doğrulanmalı: %v", m)
	}
	// Rapor içeriği değiştirilirse imza geçersiz olur.
	var pkg map[string]any
	json.Unmarshal(pkgBytes, &pkg)
	pkg["report_json"] = strings.Replace(pkg["report_json"].(string), `"partial"`, `"partial2"`, 1)
	tampered, _ := json.Marshal(pkg)
	if m := verify(tampered); m["signature_valid"] != false {
		t.Fatalf("değiştirilmiş rapor imzası geçersiz olmalı: %v", m)
	}
	// Başka anahtarla imzalanmış sahte paket: imza geçerli olabilir ama sunucuya ait değil.
	// Raporun zincir başı kaydı sonradan değiştirilirse (ve zincir yeniden yazılsa bile) paket yakalar.
	var rep evidenceReport
	json.Unmarshal([]byte(pkg["report_json"].(string)), &rep)
	d.Exec(`UPDATE audit_log SET hash = CASE WHEN left(hash,1) = 'f' THEN '0' ELSE 'f' END || substr(hash, 2) WHERE id = $1`, rep.HeadID)
	if m := verify(pkgBytes); m["chain_match"] != false {
		t.Fatalf("zincir başı değişince paket uyuşmazlık bildirmeli: %v", m)
	}
	// Genel anahtar PEM ve harici doğrulama için imza biçimi.
	c, pemb := call("GET", "/api/evidence/public-key", nil)
	if c != 200 || !strings.Contains(string(pemb), "BEGIN PUBLIC KEY") {
		t.Fatalf("pem: %d %s", c, pemb)
	}
	if _, err := base64.StdEncoding.DecodeString(pkg["signature"].(string)); err != nil {
		t.Fatal(err)
	}
	// Liste.
	c, lb := call("GET", "/api/evidence/reports", nil)
	if c != 200 || !strings.Contains(string(lb), period) {
		t.Fatalf("liste: %d %s", c, lb)
	}
}
