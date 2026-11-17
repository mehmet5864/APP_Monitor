package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"appmon/internal/agentcfg"
)

// confOffer ajana heartbeat yanıtında gönderilecek uzaktan ayarı üretir (gerek yoksa nil).
// applied: ajanın bildirdiği uygulanmış sürüm (cfg_v etiketi; boş = yok). Satır silindiyse ve ajan hâlâ uzak ayardaysa v=0 ile geri alınır.
func (s *Server) confOffer(r *http.Request, assetID int64, labels map[string]string) map[string]any {
	if labels["cfg_lock"] == "1" {
		return nil // makine yerel olarak uzaktan ayarı yasaklamış
	}
	applied, _ := strconv.Atoi(labels["cfg_v"])
	var send string
	var ver int
	var dbConf, dbPass []byte
	err := s.DB.QueryRowContext(r.Context(), `SELECT send, version, db_conf, db_pass FROM agent_config WHERE asset_id = $1`, assetID).Scan(&send, &ver, &dbConf, &dbPass)
	if err != nil { // satır yok
		if applied > 0 {
			return map[string]any{"v": 0, "send": []string{}}
		}
		return nil
	}
	if ver == applied {
		return nil
	}
	keys := []string{}
	for _, k := range strings.Split(send, ",") {
		if agentcfg.Valid(k) {
			keys = append(keys, k)
		}
	}
	out := map[string]any{"v": ver, "send": keys}
	if len(dbConf) > 0 {
		if !secureRequest(r) {
			return nil // parola düz HTTP ile gönderilmez; ajan güvenli bağlantıya kadar eski ayarda kalır
		}
		if dsn := s.agentDSN(dbConf, dbPass, assetID); dsn != "" {
			out["db"] = map[string]any{"dsn": dsn}
		}
	}
	return out
}

// secureRequest istek TLS ile (veya TLS sonlandıran proxy arkasında) ya da yerel makineden geldi mi.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	h, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		h = r.Host
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(h, "localhost")
}

type agentDBConf struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Name    string `json:"name"`
	User    string `json:"user"`
	SSLMode string `json:"sslmode"`
}

func dbAAD(assetID int64) string { return "appmon-agent-db:" + strconv.FormatInt(assetID, 10) }

func (s *Server) agentDSN(conf, sealed []byte, assetID int64) string {
	var c agentDBConf
	if json.Unmarshal(conf, &c) != nil || c.Host == "" || c.Name == "" || c.User == "" {
		return ""
	}
	pass := ""
	if len(sealed) > 0 {
		b, err := s.Vault.Open(sealed, dbAAD(assetID))
		if err != nil {
			return ""
		}
		pass = string(b)
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c.User, pass), Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Path: "/" + c.Name}
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

type agentConfRow struct {
	AssetID   int64        `json:"asset_id"`
	Name      string       `json:"name"`
	Remote    bool         `json:"remote"`    // yönetici bir ayar tanımlamış
	Version   int          `json:"version"`   // tanımlı sürüm
	Applied   int          `json:"applied"`   // ajanın uyguladığı sürüm
	Send      []string     `json:"send"`      // tanımlı seçim (remote ise)
	Reported  []string     `json:"reported"`  // ajanın şu an gönderdiği türler (SEND_ önekli)
	Locked    bool         `json:"locked"`    // makine uzaktan ayarı yasaklamış
	Supported bool         `json:"supported"` // ajan 1.3.0+ (cfg_send bildiriyor)
	UpdatedBy string       `json:"updated_by,omitempty"`
	DB        *agentDBView `json:"db,omitempty"` // kayıtlı veritabanı bağlantısı (parola asla dönmez)
}

type agentDBView struct {
	agentDBConf
	PasswordSet bool `json:"password_set"`
}

// GET /api/agent/config (yönetici): veri türleri kataloğu ve her ajanın durumu.
func (s *Server) agentConfigList(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT a.id, a.name, a.labels, c.send, c.version, c.updated_by, c.db_conf, (c.db_pass IS NOT NULL)
		   FROM assets a LEFT JOIN agent_config c ON c.asset_id = a.id
		  WHERE a.tenant_id = $1 AND a.collection = 'agent' ORDER BY a.name`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []agentConfRow{}
	for rows.Next() {
		var row agentConfRow
		var raw []byte
		var send, by *string
		var ver *int
		var dbc []byte
		var hasPass bool
		if rows.Scan(&row.AssetID, &row.Name, &raw, &send, &ver, &by, &dbc, &hasPass) != nil {
			continue
		}
		l := labelsOf(raw)
		row.Applied, _ = strconv.Atoi(l["cfg_v"])
		row.Locked = l["cfg_lock"] == "1"
		_, row.Supported = l["cfg_send"]
		row.Send, row.Reported = []string{}, []string{}
		for _, k := range strings.Split(l["cfg_send"], ",") {
			if k = strings.TrimSpace(k); k != "" && agentcfg.Valid("SEND_"+k) {
				row.Reported = append(row.Reported, "SEND_"+k)
			}
		}
		if send != nil {
			row.Remote, row.Version = true, *ver
			for _, k := range strings.Split(*send, ",") {
				if agentcfg.Valid(k) {
					row.Send = append(row.Send, k)
				}
			}
			if by != nil {
				row.UpdatedBy = *by
			}
			if len(dbc) > 0 {
				v := agentDBView{PasswordSet: hasPass}
				if json.Unmarshal(dbc, &v.agentDBConf) == nil {
					row.DB = &v
				}
			}
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{"items": agentcfg.Items, "agents": out})
}

// PUT /api/agent/config (yönetici): {"asset_ids":[…], "send":["SEND_HOST_CPU",…]} seçimi kaydeder;
// {"asset_ids":[…], "clear":true} uzaktan ayarı kaldırır (ajan yerel yapılandırmaya döner).
func (s *Server) agentConfigSet(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		AssetIDs []int64  `json:"asset_ids"`
		Send     []string `json:"send"`
		Clear    bool     `json:"clear"`
		DB       *struct {
			agentDBConf
			Password string `json:"password"`
			Remove   bool   `json:"remove"`
		} `json:"db"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.AssetIDs) == 0 || len(req.AssetIDs) > 2000 {
		fail(w, 400, "asset_ids gerekli (en fazla 2000)")
		return
	}
	if req.DB != nil && !req.Clear {
		if len(req.AssetIDs) != 1 {
			fail(w, 400, "veritabanı bağlantısı yalnızca tek bir ajan için tanımlanabilir")
			return
		}
		if !req.DB.Remove {
			c := &req.DB.agentDBConf
			c.Host, c.Name, c.User = strings.TrimSpace(c.Host), strings.TrimSpace(c.Name), strings.TrimSpace(c.User)
			if c.Port == 0 {
				c.Port = 5432
			}
			switch c.SSLMode {
			case "disable", "require", "verify-ca", "verify-full":
			case "":
				c.SSLMode = "require"
			default:
				fail(w, 400, "sslmode disable, require, verify-ca veya verify-full olmalı")
				return
			}
			if !hostRe.MatchString(c.Host) || c.Port < 1 || c.Port > 65535 || c.Name == "" || len(c.Name) > 100 || c.User == "" || len(c.User) > 100 || len(req.DB.Password) > 200 ||
				strings.ContainsAny(c.Name+c.User, "\x00\n\r") {
				fail(w, 400, "veritabanı bağlantı bilgileri geçersiz (sunucu, port, veritabanı adı, kullanıcı)")
				return
			}
			if req.DB.Password != "" && !secureRequest(r) {
				fail(w, 400, "parola yalnızca HTTPS ile kaydedilebilir (sunucuya https:// adresiyle bağlanın)")
				return
			}
		}
	}
	uniq := map[string]bool{}
	for _, k := range req.Send {
		if !agentcfg.Valid(k) {
			fail(w, 400, "bilinmeyen veri türü: "+k)
			return
		}
		uniq[k] = true
	}
	keys := make([]string, 0, len(uniq))
	for k := range uniq {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var n int64
	for _, id := range req.AssetIDs {
		if req.Clear {
			res, err := s.DB.ExecContext(r.Context(),
				`DELETE FROM agent_config WHERE asset_id = $1 AND tenant_id = $2`, id, a.TenantID)
			if err == nil {
				k, _ := res.RowsAffected()
				n += k
			}
			continue
		}
		res, err := s.DB.ExecContext(r.Context(),
			`INSERT INTO agent_config (asset_id, tenant_id, send, version, updated_by)
			 SELECT id, tenant_id, $3, 1, $4 FROM assets WHERE id = $1 AND tenant_id = $2 AND collection = 'agent'
			 ON CONFLICT (asset_id) DO UPDATE SET send = EXCLUDED.send, version = agent_config.version + 1, updated_at = now(), updated_by = EXCLUDED.updated_by`,
			id, a.TenantID, strings.Join(keys, ","), a.Label)
		if err == nil {
			k, _ := res.RowsAffected()
			n += k
		}
	}
	dbNote := ""
	if req.DB != nil && !req.Clear && n > 0 {
		id := req.AssetIDs[0]
		if req.DB.Remove {
			_, _ = s.DB.ExecContext(r.Context(), `UPDATE agent_config SET db_conf = NULL, db_pass = NULL WHERE asset_id = $1 AND tenant_id = $2`, id, a.TenantID)
			dbNote = " | veritabanı bağlantısı kaldırıldı"
		} else {
			cj, _ := json.Marshal(req.DB.agentDBConf)
			var sealed any // nil: mevcut parola korunur
			if req.DB.Password != "" {
				sealed = s.Vault.Seal([]byte(req.DB.Password), dbAAD(id))
			}
			if _, err := s.DB.ExecContext(r.Context(),
				`UPDATE agent_config SET db_conf = $3, db_pass = CASE WHEN $4::bytea IS NOT NULL THEN $4::bytea ELSE db_pass END WHERE asset_id = $1 AND tenant_id = $2`,
				id, a.TenantID, string(cj), sealed); err != nil {
				fail(w, 500, "veritabanı hatası")
				return
			}
			dbNote = " | veritabanı: " + req.DB.User + "@" + req.DB.Host + ":" + strconv.Itoa(req.DB.Port) + "/" + req.DB.Name
			if req.DB.Password != "" {
				dbNote += " (parola güncellendi)"
			}
		}
	}
	act := "agent.config_set"
	detail := strconv.FormatInt(n, 10) + " ajan: " + strings.Join(keys, ",") + dbNote
	if req.Clear {
		act, detail = "agent.config_clear", strconv.FormatInt(n, 10)+" ajan"
	}
	s.audit(r.Context(), a.TenantID, a.Label, act, detail)
	writeJSON(w, 200, map[string]any{"updated": n})
}

func labelsOf(raw []byte) map[string]string {
	l := map[string]string{}
	_ = json.Unmarshal(raw, &l)
	return l
}
