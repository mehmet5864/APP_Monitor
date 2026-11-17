package app

import (
	"context"
	"log"
	"math"

	"github.com/lib/pq"
)

// anomalyMetrics hangi ölçüm için hangi anomali skoru metriğinin üretileceğini belirler.
var anomalyMetrics = map[string]string{
	"probe.latency_ms": "anomaly.latency_z",
	"host.cpu_pct":     "anomaly.cpu_z",
	"host.mem_pct":     "anomaly.mem_z",
}

// anomalyZ güçlü (robust) z-skoru: (şimdiki - medyan) / (1.4826 * MAD). Çok durağan serilerde küçük sapmalar
// sonsuz skor üretmesin diye sigma, medyanın %10'u ve küçük bir mutlak değerle tabanlanır.
func anomalyZ(cur, median, mad float64) float64 {
	sigma := math.Max(1.4826*mad, math.Max(0.1*math.Abs(median), 1e-3))
	return (cur - median) / sigma
}

// anomalyJob son 5 dakikanın ortalamasını, son 30 dakika hariç 7 günlük geçmişle karşılaştırır ve
// anomaly.*_z metriklerini yazar. Alarm kuralları bu metrikler üzerinden yazılır (ör. anomaly.latency_z > 6).
func (s *Server) anomalyJob(ctx context.Context) {
	names := make([]string, 0, len(anomalyMetrics))
	for k := range anomalyMetrics {
		names = append(names, k)
	}
	rows, err := s.DB.QueryContext(ctx, `
WITH base AS (
  SELECT asset_id, name, value FROM metrics
   WHERE name = ANY($1) AND time > now() - interval '7 days' AND time < now() - interval '30 minutes'
), med AS (
  SELECT asset_id, name, percentile_cont(0.5) WITHIN GROUP (ORDER BY value) AS m
    FROM base GROUP BY asset_id, name HAVING count(*) >= 300
), mad AS (
  SELECT b.asset_id, b.name, percentile_cont(0.5) WITHIN GROUP (ORDER BY abs(b.value - med.m)) AS d
    FROM base b JOIN med USING (asset_id, name) GROUP BY b.asset_id, b.name
), cur AS (
  SELECT asset_id, name, avg(value) AS c, count(*) AS k FROM metrics
   WHERE name = ANY($1) AND time > now() - interval '5 minutes' GROUP BY asset_id, name
)
SELECT med.asset_id, a.tenant_id, med.name, cur.c, med.m, mad.d
  FROM med JOIN mad USING (asset_id, name) JOIN cur USING (asset_id, name) JOIN assets a ON a.id = med.asset_id
 WHERE cur.k >= 2`, pq.Array(names))
	if err != nil {
		log.Printf("anomali sorgusu: %v", err)
		return
	}
	type rec struct {
		asset, tenant int64
		name          string
		z             float64
	}
	var out []rec
	for rows.Next() {
		var r rec
		var cur, m, d float64
		if rows.Scan(&r.asset, &r.tenant, &r.name, &cur, &m, &d) == nil {
			r.z = anomalyZ(cur, m, d)
			out = append(out, r)
		}
	}
	rows.Close()
	for _, r := range out {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES (now(), $1, $2, $3, $4)`,
			r.tenant, r.asset, anomalyMetrics[r.name], r.z); err != nil {
			log.Printf("anomali yazılamadı: %v", err)
			return
		}
	}
}
