package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"appmon/internal/ldap"
)

// ignoreRule sayfa değişikliği karşılaştırmasından çıkarılan bir öğeyi veya değeri tanımlar.
//
//	item : A ile birebir eşleşen öğe iki taraftan da atılır (yapısı da değişen reklam, banner vb.).
//	value: A ile başlayıp B ile biten öğenin arası "~" yapılır; öğe sayfada durmaya devam etmelidir
//	       (kaybolursa gerçek değişiklik sayılır), yalnızca değeri değişince alarm çıkmaz (sayaç, token, tarih).
type ignoreRule struct {
	ID   int64  `json:"id"`
	Step int    `json:"step"`
	Kind string `json:"kind"`
	A    string `json:"a"`
	B    string `json:"b,omitempty"`
	By   string `json:"by,omitempty"`
	At   string `json:"at,omitempty"`
}

const (
	maxIgnores   = 200
	maxIgnoreLen = 400
)

// applyIgnores kuralları öğe listesine uygular; elenen veya maskelenen öğe sayısını da döndürür.
func applyIgnores(items []string, rules []ignoreRule) ([]string, int) {
	if len(rules) == 0 {
		return items, 0
	}
	out := make([]string, 0, len(items))
	n := 0
next:
	for _, it := range items {
		for _, r := range rules {
			switch r.Kind {
			case "item":
				if it == r.A {
					n++
					continue next
				}
			case "value":
				if len(it) >= len(r.A)+len(r.B) && strings.HasPrefix(it, r.A) && strings.HasSuffix(it, r.B) {
					masked := r.A + "~" + r.B
					if masked != it {
						n++
					}
					it = masked
				}
			}
		}
		out = append(out, it)
	}
	return out, n
}

func wordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-' || c == '+' || c == '=' || c >= 0x80
}

var tokenRun = regexp.MustCompile(`[A-Za-z0-9_\-+/=.]{12,}`)

// buildValueRule maskelenecek parçayı bulur. other (karşı taraftaki benzer öğe: eklenene karşılık kaldırılan) varsa
// ortak ön ek ve son ek korunur, aradaki fark maskelenir; yoksa öğedeki en uzun token benzeri dizi maskelenir.
func buildValueRule(item, other string) (a, b string, err error) {
	if other != "" && other != item {
		p := 0
		for p < len(item) && p < len(other) && item[p] == other[p] {
			p++
		}
		s := 0
		for s < len(item)-p && s < len(other)-p && item[len(item)-1-s] == other[len(other)-1-s] {
			s++
		}
		// Rastgele ortak karakterler kuralı kırılgan yapar: ön/son ek sözcük (token) sınırına geri çekilir.
		for p > 0 && wordByte(item[p-1]) {
			p--
		}
		for s > 0 && wordByte(item[len(item)-s]) {
			s--
		}
		a, b = item[:p], item[len(item)-s:]
	} else {
		loc := tokenRun.FindAllStringIndex(item, -1)
		best := [2]int{-1, -1}
		for _, l := range loc {
			if best[0] < 0 || l[1]-l[0] > best[1]-best[0] {
				best = [2]int{l[0], l[1]}
			}
		}
		if best[0] < 0 {
			return "", "", errors.New("öğede maskelenecek bir değer bulunamadı; \"Öğeyi yoksay\" seçeneğini kullanın")
		}
		a, b = item[:best[0]], item[best[1]:]
	}
	// Ön ek "tür:" kısmını (örn. "a:", "h:h1:") aşmalı ve çok kısa olmamalı: aksi hâlde tüm öğeleri maskeler.
	if strings.Count(a, ":") < 1 || len(a)+len(b) < 6 {
		return "", "", errors.New("değer güvenle maskelenemedi (ortak kısım çok kısa); \"Öğeyi yoksay\" seçeneğini kullanın")
	}
	return a, b, nil
}

func (s *Server) loadIgnores(ctx context.Context, scenarioID int64) map[int][]ignoreRule {
	out := map[int][]ignoreRule{}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, step_idx, kind, a, b FROM scenario_ignores WHERE scenario_id = $1 ORDER BY id`, scenarioID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r ignoreRule
		if rows.Scan(&r.ID, &r.Step, &r.Kind, &r.A, &r.B) == nil {
			out[r.Step] = append(out[r.Step], r)
		}
	}
	return out
}

// reauth riskli bir işlem öncesi kimliği yeniden doğrular: hesap oturumunda parola (yerel veya LDAP),
// API anahtarıyla girişte aynı anahtarın yeniden girilmesi. Başarısız denemeler IP başına sınırlanır.
func (s *Server) reauth(r *http.Request, a Auth, password, apiKey string) (int, error) {
	ip := clientIP(r)
	if ipBlocked(ip) {
		return http.StatusTooManyRequests, errors.New("çok fazla başarısız deneme; birkaç dakika sonra tekrar deneyin")
	}
	bad := func() (int, error) {
		ipFail(ip)
		s.audit(r.Context(), a.TenantID, a.Label, "reauth.fail", ip)
		return http.StatusForbidden, errors.New("doğrulama başarısız")
	}
	if len(password) > 256 || len(apiKey) > 256 {
		return bad()
	}
	if a.UserID == 0 {
		cur := r.Header.Get("X-API-Key")
		if cur == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				cur = strings.TrimSpace(h[7:])
			}
		}
		if apiKey == "" || subtle.ConstantTimeCompare([]byte(HashKey(apiKey)), []byte(HashKey(cur))) != 1 {
			return bad()
		}
		return 0, nil
	}
	var hash, src, name string
	var enabled bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT pass_hash, source, username, enabled FROM users WHERE id = $1`, a.UserID).Scan(&hash, &src, &name, &enabled); err != nil || !enabled {
		return http.StatusForbidden, errors.New("kullanıcı bulunamadı")
	}
	if password == "" {
		return bad()
	}
	ok := false
	switch src {
	case "local":
		ok = checkPassword(password, hash)
	case "ldap":
		if cfg, on := s.ldapConfig(); on && ldap.ValidUser(name) {
			ok = ldap.Authenticate(cfg, name, password) == nil
		}
	}
	if !ok {
		return bad()
	}
	return 0, nil
}

// ---- API ----

func (s *Server) listIgnores(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT i.id, i.step_idx, i.kind, i.a, i.b, i.created_by, to_char(i.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		   FROM scenario_ignores i JOIN scenarios s ON s.id = i.scenario_id WHERE i.scenario_id = $1 AND s.tenant_id = $2 ORDER BY i.id`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []ignoreRule{}
	for rows.Next() {
		var x ignoreRule
		if rows.Scan(&x.ID, &x.Step, &x.Kind, &x.A, &x.B, &x.By, &x.At) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) addIgnore(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		Step     int    `json:"step"`
		Kind     string `json:"kind"`
		Item     string `json:"item"`
		Other    string `json:"other"`
		Password string `json:"password"`
		APIKey   string `json:"api_key"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Step < 0 || req.Step > 500 || (req.Kind != "item" && req.Kind != "value") || req.Item == "" || len(req.Item) > maxIgnoreLen || len(req.Other) > maxIgnoreLen {
		fail(w, 400, "geçersiz istek")
		return
	}
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT name FROM scenarios WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&name); err != nil {
		fail(w, 404, "senaryo bulunamadı")
		return
	}
	if code, err := s.reauth(r, a, req.Password, req.APIKey); err != nil {
		fail(w, code, err.Error())
		return
	}
	ra, rb := req.Item, ""
	if req.Kind == "value" {
		var err error
		if ra, rb, err = buildValueRule(req.Item, req.Other); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	var n int
	s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM scenario_ignores WHERE scenario_id = $1`, id).Scan(&n)
	if n >= maxIgnores {
		fail(w, 400, fmt.Sprintf("bir senaryoda en çok %d yoksayma kuralı olabilir", maxIgnores))
		return
	}
	var rid int64
	err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO scenario_ignores (scenario_id, step_idx, kind, a, b, created_by) VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (scenario_id, step_idx, kind, a, b) DO UPDATE SET created_by = scenario_ignores.created_by RETURNING id`,
		id, req.Step, req.Kind, ra, rb, a.Label).Scan(&rid)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "scenario.ignore_add",
		fmt.Sprintf("%s adım %d %s: %q", name, req.Step+1, req.Kind, ra+map[bool]string{true: "~" + rb, false: ""}[req.Kind == "value"]))
	writeJSON(w, 201, ignoreRule{ID: rid, Step: req.Step, Kind: req.Kind, A: ra, B: rb, By: a.Label})
}

func (s *Server) deleteIgnore(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	rid, e2 := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if !ok || e2 != nil {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(),
		`DELETE FROM scenario_ignores WHERE id = $1 AND scenario_id = $2 AND scenario_id IN (SELECT id FROM scenarios WHERE tenant_id = $3)`, rid, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "kural bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "scenario.ignore_del", fmt.Sprintf("senaryo %d kural %d", id, rid))
	w.WriteHeader(http.StatusNoContent)
}
