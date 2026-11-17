package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const defaultSLATarget = 99.9

// slaDayQuery bir günün (yerel saat) asset başına gözlem/kesinti/kısmi kesinti saniyesini ve olay sayısını hesaplar.
// Her ölçümün süresi, önceki ölçümden bu yana geçen süredir (en çok 300 sn); böylece aralığı farklı probe'lar doğru ağırlıklanır.
// Bakım penceresine denk gelen kesinti saniyeleri ayrıca sayılır ve rapordan düşülür.
const slaDayQuery = `
WITH s AS (
  SELECT m.asset_id, m.name, m.time, m.value,
         LAG(m.time)  OVER w AS pt, LAG(m.value) OVER w AS pv
    FROM metrics m
   WHERE m.tenant_id = $1 AND m.name IN ('asset.down', 'asset.degraded')
     AND m.time >= $2::timestamptz - interval '10 minutes' AND m.time < $3
  WINDOW w AS (PARTITION BY m.asset_id, m.name ORDER BY m.time)
), g AS (
  SELECT s.asset_id, s.name, s.value, s.pv, s.time,
         to_timestamp(floor(extract(epoch FROM s.time) / 3600) * 3600) AS hr,
         LEAST(extract(epoch FROM s.time - s.pt), 300) AS dt,
         EXISTS (
           SELECT 1 FROM maintenance_windows w
            WHERE w.tenant_id = $1
              AND (cardinality(w.asset_ids) = 0 AND w.group_name = '' AND w.tag = '' OR a.id = ANY(w.asset_ids)
                   OR (w.group_name <> '' AND a.grp = w.group_name) OR (w.tag <> '' AND w.tag = ANY(a.tags)))
              AND ((w.repeat = '' AND s.time BETWEEN w.starts_at AND w.ends_at)
                OR (w.repeat = 'daily' AND s.time >= w.starts_at AND
                    mod(extract(epoch FROM s.time - w.starts_at)::bigint, 86400) <= extract(epoch FROM w.ends_at - w.starts_at))
                OR (w.repeat = 'weekly' AND s.time >= w.starts_at AND
                    mod(extract(epoch FROM s.time - w.starts_at)::bigint, 604800) <= extract(epoch FROM w.ends_at - w.starts_at)))
         ) AS maint
    FROM s JOIN assets a ON a.id = s.asset_id
   WHERE s.pt IS NOT NULL AND s.time >= $2
     AND s.pt >= COALESCE(a.sla_reset_at, '-infinity'::timestamptz)
)
SELECT asset_id, hr,
       COALESCE(sum(dt) FILTER (WHERE name = 'asset.down'), 0)::int,
       COALESCE(sum(dt) FILTER (WHERE name = 'asset.down' AND value = 1), 0)::int,
       COALESCE(sum(dt) FILTER (WHERE name = 'asset.down' AND value = 1 AND maint), 0)::int,
       COALESCE(sum(dt) FILTER (WHERE name = 'asset.degraded' AND value = 1), 0)::int,
       count(*) FILTER (WHERE name = 'asset.down' AND value = 1 AND pv = 0 AND NOT maint)::int
  FROM g GROUP BY asset_id, hr`

// rollupSLA [from, to] aralığındaki her günü yeniden hesaplayıp sla_daily'ye yazar.
func (s *Server) rollupSLA(ctx context.Context, tenant int64, from, to time.Time) error {
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.Local)
	for ; !day.After(end); day = day.AddDate(0, 0, 1) {
		next := day.AddDate(0, 0, 1)
		rows, err := s.DB.QueryContext(ctx, slaDayQuery, tenant, day, next)
		if err != nil {
			return err
		}
		type rec struct {
			asset                      int64
			hr                         time.Time
			obs, down, maint, deg, inc int
		}
		var list []rec
		for rows.Next() {
			var r rec
			if err := rows.Scan(&r.asset, &r.hr, &r.obs, &r.down, &r.maint, &r.deg, &r.inc); err == nil {
				list = append(list, r)
			}
		}
		rows.Close()
		daily := map[int64]*rec{}
		for _, r := range list {
			lt := r.hr.In(time.Local)
			if _, err := s.DB.ExecContext(ctx,
				`INSERT INTO sla_hourly (tenant_id, asset_id, hr, hod, dow, observed_s, down_s, maint_s, degraded_s, incidents)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				 ON CONFLICT (asset_id, hr) DO UPDATE SET observed_s = EXCLUDED.observed_s, down_s = EXCLUDED.down_s,
				   maint_s = EXCLUDED.maint_s, degraded_s = EXCLUDED.degraded_s, incidents = EXCLUDED.incidents`,
				tenant, r.asset, r.hr, lt.Hour(), int(lt.Weekday()), r.obs, r.down, r.maint, r.deg, r.inc); err != nil {
				return err
			}
			d := daily[r.asset]
			if d == nil {
				d = &rec{asset: r.asset}
				daily[r.asset] = d
			}
			d.obs, d.down, d.maint, d.deg, d.inc = d.obs+r.obs, d.down+r.down, d.maint+r.maint, d.deg+r.deg, d.inc+r.inc
		}
		for _, r := range daily {
			if _, err := s.DB.ExecContext(ctx,
				`INSERT INTO sla_daily (tenant_id, asset_id, day, observed_s, down_s, maint_s, degraded_s, incidents)
				 VALUES ($1,$2,$3::date,$4,$5,$6,$7,$8)
				 ON CONFLICT (asset_id, day) DO UPDATE SET observed_s = EXCLUDED.observed_s, down_s = EXCLUDED.down_s,
				   maint_s = EXCLUDED.maint_s, degraded_s = EXCLUDED.degraded_s, incidents = EXCLUDED.incidents`,
				tenant, r.asset, day.Format("2006-01-02"), r.obs, r.down, r.maint, r.deg, r.inc); err != nil {
				return err
			}
		}
	}
	return nil
}

// slaJob her kiracı için bugünü ve dünü yeniler; ilk çalıştırmada ham ölçümlerin tutulduğu tüm günleri doldurur.
func (s *Server) slaJob(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM tenants`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	now := time.Now()
	from := now.AddDate(0, 0, -1)
	if !s.slaBackfilled {
		from = now.AddDate(0, 0, -s.RetentionDays)
	}
	ok := true
	for _, t := range ids {
		if err := s.rollupSLA(ctx, t, from, now); err != nil {
			log.Printf("SLA özeti: %v", err)
			ok = false
		}
	}
	if ok {
		s.slaBackfilled = true
	}
	s.DB.ExecContext(ctx, `DELETE FROM sla_daily WHERE day < now()::date - 800`)
	s.DB.ExecContext(ctx, `DELETE FROM sla_hourly WHERE hr < now() - interval '400 days'`)
}

type slaRow struct {
	AssetID   int64   `json:"asset_id"`
	Name      string  `json:"name"`
	Group     string  `json:"group"`
	Target    float64 `json:"target_pct"`
	Custom    bool    `json:"custom_target"`
	Observed  int     `json:"observed_s"`
	Down      int     `json:"down_s"`
	Maint     int     `json:"maint_s"`
	Degraded  int     `json:"degraded_s"`
	Incidents int     `json:"incidents"`
	Avail     float64 `json:"availability_pct"`
	Met       bool    `json:"met"`
}

func (s *Server) slaReport(ctx context.Context, tenant int64, from, to time.Time) ([]slaRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT a.id, a.name, a.grp, a.sla_target, sum(d.observed_s), sum(d.down_s), sum(d.maint_s), sum(d.degraded_s), sum(d.incidents)
		  FROM sla_daily d JOIN assets a ON a.id = d.asset_id
		 WHERE d.tenant_id = $1 AND d.day >= $2::date AND d.day <= $3::date
		 GROUP BY a.id ORDER BY a.name`, tenant, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []slaRow{}
	for rows.Next() {
		var r slaRow
		var tgt *float64
		if err := rows.Scan(&r.AssetID, &r.Name, &r.Group, &tgt, &r.Observed, &r.Down, &r.Maint, &r.Degraded, &r.Incidents); err != nil {
			return nil, err
		}
		r.Target, r.Custom = defaultSLATarget, false
		if tgt != nil {
			r.Target, r.Custom = *tgt, true
		}
		r.Avail = slaAvail(r.Observed, r.Down, r.Maint)
		r.Met = r.Avail >= r.Target
		out = append(out, r)
	}
	return out, rows.Err()
}

// slaAvail: bakım sırasındaki kesinti hem kesintiden hem gözlem süresinden düşülür.
func slaAvail(observed, down, maint int) float64 {
	obs := observed - maint
	if obs <= 0 {
		return 100
	}
	d := down - maint
	if d < 0 {
		d = 0
	}
	return 100 * (1 - float64(d)/float64(obs))
}

// slaFilter SLA raporunun zaman süzgeçleridir. Süzgeç yoksa (7/24, tam gün aralığı) günlük özet tablosu kullanılır.
type slaFilter struct {
	From, To   time.Time // [From, To)
	DateOnly   bool      // from/to yalnızca tarih (tam gün) verildi
	HourFrom   int       // yerel saat penceresi [HourFrom, HourTo); 0-24 = tüm gün
	HourTo     int
	Days       []int // haftanın günleri (0 = Pazar); boş = hepsi
	Group      string
	AssetID    int64
	hasWindow  bool
	windowText string
}

// parseSLAFilter from/to (YYYY-AA-GG veya YYYY-AA-GGTSS:DD), hours (8-17, 7-24, 22-6), days (1,2,3,4,5) ve group (none|hour|day|week) alanlarını okur.
func parseSLAFilter(r *http.Request) (slaFilter, error) {
	q := r.URL.Query()
	now := time.Now()
	f := slaFilter{From: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), To: now, HourFrom: 0, HourTo: 24, Group: "none", DateOnly: true}
	parse := func(v, name string, end bool) (time.Time, bool, error) {
		if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
			if end {
				t = t.AddDate(0, 0, 1)
			}
			return t, true, nil
		}
		if t, err := time.ParseInLocation("2006-01-02T15:04", v, time.Local); err == nil {
			return t, false, nil
		}
		return time.Time{}, false, fmt.Errorf("%s YYYY-AA-GG veya YYYY-AA-GGTSS:DD biçiminde olmalı", name)
	}
	if v := q.Get("from"); v != "" {
		t, d, err := parse(v, "from", false)
		if err != nil {
			return f, err
		}
		f.From, f.DateOnly = t, f.DateOnly && d
	}
	if v := q.Get("to"); v != "" {
		t, d, err := parse(v, "to", true)
		if err != nil {
			return f, err
		}
		f.To, f.DateOnly = t, f.DateOnly && d
	} else {
		f.To, f.DateOnly = now, false
	}
	if !f.To.After(f.From) || f.To.Sub(f.From) > 401*24*time.Hour {
		return f, fmt.Errorf("tarih aralığı geçersiz (en çok 400 gün)")
	}
	if v := q.Get("hours"); v != "" {
		var a, b int
		if n, err := fmt.Sscanf(v, "%d-%d", &a, &b); n != 2 || err != nil || a < 0 || a > 23 || b < 0 || b > 24 || a == b {
			return f, fmt.Errorf("hours 8-17, 7-24 veya 22-6 biçiminde olmalı (başlangıç 0-23, bitiş 0-24)")
		}
		f.HourFrom, f.HourTo = a, b
		f.hasWindow = !(a == 0 && b == 24)
	}
	if v := q.Get("days"); v != "" {
		seen := map[int]bool{}
		for _, p := range strings.Split(v, ",") {
			d, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || d < 0 || d > 6 {
				return f, fmt.Errorf("days 0-6 (0 = Pazar) değerlerinin virgülle ayrılmış listesi olmalı")
			}
			if !seen[d] {
				seen[d] = true
				f.Days = append(f.Days, d)
			}
		}
		sort.Ints(f.Days)
		if len(f.Days) < 7 {
			f.hasWindow = true
		} else {
			f.Days = nil
		}
	}
	switch g := q.Get("group"); g {
	case "", "none":
	case "hour", "day", "week":
		f.Group = g
	default:
		return f, fmt.Errorf("group none, hour, day veya week olmalı")
	}
	if v := q.Get("asset"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return f, fmt.Errorf("asset geçersiz")
		}
		f.AssetID = id
	}
	return f, nil
}

// legacy: süzgeç yok ve aralık tam günlerden oluşuyorsa eski (günlük) yol kullanılır; saatlik veri yalnızca yakın geçmişi kapsar.
func (f slaFilter) legacy() bool {
	return !f.hasWindow && f.DateOnly && f.Group == "none" && f.AssetID == 0
}

// where saatlik tablo için süzgeç koşulunu ve parametrelerini üretir ($1 kiracı, $2 başlangıç, $3 bitiş).
func (f slaFilter) where(tenant int64) (string, []any) {
	cond := "h.tenant_id = $1 AND h.hr >= $2 AND h.hr < $3"
	args := []any{tenant, f.From, f.To}
	if f.HourFrom != 0 || f.HourTo != 24 {
		if f.HourFrom < f.HourTo {
			cond += fmt.Sprintf(" AND h.hod >= %d AND h.hod < %d", f.HourFrom, f.HourTo)
		} else {
			cond += fmt.Sprintf(" AND (h.hod >= %d OR h.hod < %d)", f.HourFrom, f.HourTo)
		}
	}
	if len(f.Days) > 0 {
		cond += " AND h.dow = ANY($4)"
		args = append(args, pq.Array(f.Days))
	}
	return cond, args
}

type slaPoint struct {
	T         string  `json:"t"`
	Label     string  `json:"label"`
	Observed  int     `json:"observed_s"`
	Down      int     `json:"down_s"`
	Maint     int     `json:"maint_s"`
	Incidents int     `json:"incidents"`
	Avail     float64 `json:"availability_pct"`
}

// slaReportFiltered saatlik özetten, çalışma saati/gün süzgeciyle asset başına rapor üretir.
func (s *Server) slaReportFiltered(ctx context.Context, tenant int64, f slaFilter) ([]slaRow, error) {
	cond, args := f.where(tenant)
	if f.AssetID != 0 {
		args = append(args, f.AssetID)
		cond += fmt.Sprintf(" AND h.asset_id = $%d", len(args))
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT a.id, a.name, a.grp, a.sla_target, sum(h.observed_s), sum(h.down_s), sum(h.maint_s), sum(h.degraded_s), sum(h.incidents)
		  FROM sla_hourly h JOIN assets a ON a.id = h.asset_id
		 WHERE `+cond+` GROUP BY a.id ORDER BY a.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []slaRow{}
	for rows.Next() {
		var r slaRow
		var tgt *float64
		if err := rows.Scan(&r.AssetID, &r.Name, &r.Group, &tgt, &r.Observed, &r.Down, &r.Maint, &r.Degraded, &r.Incidents); err != nil {
			return nil, err
		}
		r.Target, r.Custom = defaultSLATarget, false
		if tgt != nil {
			r.Target, r.Custom = *tgt, true
		}
		r.Avail = slaAvail(r.Observed, r.Down, r.Maint)
		r.Met = r.Avail >= r.Target
		out = append(out, r)
	}
	return out, rows.Err()
}

// slaSeries seçili asset'lerin (veya tümünün) toplam erişilebilirliğini saatlik/günlük/haftalık kovalarda verir.
func (s *Server) slaSeries(ctx context.Context, tenant int64, f slaFilter) ([]slaPoint, error) {
	cond, args := f.where(tenant)
	if f.AssetID != 0 {
		args = append(args, f.AssetID)
		cond += fmt.Sprintf(" AND h.asset_id = $%d", len(args))
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT h.hr, sum(h.observed_s), sum(h.down_s), sum(h.maint_s), sum(h.incidents)
		  FROM sla_hourly h WHERE `+cond+` GROUP BY h.hr ORDER BY h.hr`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		label                 string
		obs, down, maint, inc int
	}
	order := []string{}
	m := map[string]*acc{}
	for rows.Next() {
		var hr time.Time
		var o, d, mt, in int
		if err := rows.Scan(&hr, &o, &d, &mt, &in); err != nil {
			return nil, err
		}
		lt := hr.In(time.Local)
		var key, label string
		switch f.Group {
		case "hour":
			key, label = lt.Format("2006-01-02T15:00"), lt.Format("02.01 15:00")
		case "day":
			key, label = lt.Format("2006-01-02"), lt.Format("02.01.2006")
		default: // week: Pazartesi başlangıçlı
			off := (int(lt.Weekday()) + 6) % 7
			ws := time.Date(lt.Year(), lt.Month(), lt.Day()-off, 0, 0, 0, 0, time.Local)
			key, label = ws.Format("2006-01-02"), ws.Format("02.01.2006")+" haftası"
		}
		a := m[key]
		if a == nil {
			a = &acc{label: label}
			m[key] = a
			order = append(order, key)
		}
		a.obs, a.down, a.maint, a.inc = a.obs+o, a.down+d, a.maint+mt, a.inc+in
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]slaPoint, 0, len(order))
	for _, k := range order {
		a := m[k]
		out = append(out, slaPoint{T: k, Label: a.label, Observed: a.obs, Down: a.down, Maint: a.maint, Incidents: a.inc, Avail: slaAvail(a.obs, a.down, a.maint)})
	}
	return out, nil
}

func (f slaFilter) describe() map[string]any {
	days := f.Days
	if days == nil {
		days = []int{}
	}
	return map[string]any{"hours": fmt.Sprintf("%d-%d", f.HourFrom, f.HourTo), "days": days, "group": f.Group}
}

func (s *Server) slaAny(ctx context.Context, tenant int64, f slaFilter) ([]slaRow, error) {
	if f.legacy() {
		return s.slaReport(ctx, tenant, f.From, f.To.AddDate(0, 0, -1))
	}
	return s.slaReportFiltered(ctx, tenant, f)
}

func (s *Server) getSLA(w http.ResponseWriter, r *http.Request, a Auth) {
	f, err := parseSLAFilter(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	list, err := s.slaAny(r.Context(), a.TenantID, f)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	out := map[string]any{"from": f.From.Format("2006-01-02T15:04"), "to": f.To.Format("2006-01-02T15:04"), "default_target": defaultSLATarget, "rows": list, "filter": f.describe(), "legacy": f.legacy()}
	var first *time.Time
	s.DB.QueryRowContext(r.Context(), `SELECT min(hr) FROM sla_hourly WHERE tenant_id = $1`, a.TenantID).Scan(&first)
	if first != nil {
		out["hourly_from"] = first.Local().Format("2006-01-02T15:04")
	}
	if f.Group != "none" {
		series, err := s.slaSeries(r.Context(), a.TenantID, f)
		if err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		out["series"] = series
	}
	writeJSON(w, 200, out)
}

func (s *Server) getSLACSV(w http.ResponseWriter, r *http.Request, a Auth) {
	f, err := parseSLAFilter(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	list, err := s.slaAny(r.Context(), a.TenantID, f)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="sla-%s-%s.csv"`, f.From.Format("20060102"), f.To.Format("20060102")))
	w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	if f.hasWindow {
		days := "her gün"
		if len(f.Days) > 0 {
			days = fmt.Sprint(f.Days)
		}
		cw.Write([]string{"Zaman filtresi", fmt.Sprintf("%02d:00-%02d:00", f.HourFrom, f.HourTo), "Günler (0=Pazar)", days})
	}
	cw.Write([]string{"Asset", "Grup", "Hedef %", "Erişilebilirlik %", "Hedef karşılandı", "Kesinti (dk)", "Bakımda kesinti (dk)", "Kısmi kesinti (dk)", "Olay sayısı", "Gözlem (saat)"})
	fm := func(v float64, p int) string { return strconv.FormatFloat(v, 'f', p, 64) }
	for _, x := range list {
		met := "Evet"
		if !x.Met {
			met = "Hayır"
		}
		net := x.Down - x.Maint
		if net < 0 {
			net = 0
		}
		cw.Write([]string{csvSafe(x.Name), csvSafe(x.Group), fm(x.Target, 3), fm(x.Avail, 3), met, fm(float64(net)/60, 1), fm(float64(x.Maint)/60, 1), fm(float64(x.Degraded)/60, 1), strconv.Itoa(x.Incidents), fm(float64(x.Observed-x.Maint)/3600, 1)})
	}
	cw.Flush()
}

// POST /api/sla/reset {asset_ids: [..] | all: true, password | api_key}
// Yönetici parolası (API anahtarıyla girenlerde anahtar) yeniden istenir. Seçilen asset'lerin SLA özetleri silinir ve hesaplamaya
// bu andan itibaren başlanır; ham ölçümler, alarmlar ve denetim kaydı etkilenmez.
func (s *Server) resetSLA(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		AssetIDs []int64 `json:"asset_ids"`
		All      bool    `json:"all"`
		Password string  `json:"password"`
		APIKey   string  `json:"api_key"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.All && len(req.AssetIDs) == 0 {
		fail(w, 400, "asset_ids veya all gerekli")
		return
	}
	if len(req.AssetIDs) > 1000 {
		fail(w, 400, "en çok 1000 asset")
		return
	}
	if code, err := s.reauth(r, a, req.Password, req.APIKey); err != nil {
		fail(w, code, err.Error())
		return
	}
	ctx := r.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	sel := `tenant_id = $1 AND ($2::boolean OR id = ANY($3))`
	var ids []int64
	rows, err := tx.QueryContext(ctx, `SELECT id FROM assets WHERE `+sel, a.TenantID, req.All, pq.Array(req.AssetIDs))
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		fail(w, 404, "asset bulunamadı")
		return
	}
	for _, q := range []string{
		`UPDATE assets SET sla_reset_at = now() WHERE tenant_id = $1 AND id = ANY($2)`,
		`DELETE FROM sla_daily WHERE tenant_id = $1 AND asset_id = ANY($2)`,
		`DELETE FROM sla_hourly WHERE tenant_id = $1 AND asset_id = ANY($2)`,
	} {
		if _, err := tx.ExecContext(ctx, q, a.TenantID, pq.Array(ids)); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	detail := fmt.Sprintf("%d asset", len(ids))
	if len(ids) == 1 {
		detail = fmt.Sprintf("asset %d", ids[0])
	}
	s.audit(ctx, a.TenantID, a.Label, "sla_reset", detail)
	writeJSON(w, 200, map[string]any{"status": "ok", "reset": len(ids)})
}

// csvSafe elektronik tabloda formül olarak çalışmasın diye = + - @ ile başlayan metinlerin başına ' koyar.
func csvSafe(v string) string {
	if v != "" && (v[0] == '=' || v[0] == '+' || v[0] == '-' || v[0] == '@') {
		return "'" + v
	}
	return v
}

func (s *Server) setSLATarget(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Target *float64 `json:"target_pct"` // null = varsayılana dön
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Target != nil && (*req.Target < 50 || *req.Target > 100) {
		fail(w, 400, "hedef %50 ile %100 arasında olmalı")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE assets SET sla_target = $1 WHERE id = $2 AND tenant_id = $3`, req.Target, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "asset bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "sla_target", fmt.Sprintf("asset %d", id))
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
