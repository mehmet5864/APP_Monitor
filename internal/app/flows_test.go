package app

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func v5Pkt(recs ...[3]any) []byte { // {srcIP, dstIP, bytes}; dstport 443 tcp
	h := make([]byte, 24)
	binary.BigEndian.PutUint16(h[0:], 5)
	binary.BigEndian.PutUint16(h[2:], uint16(len(recs)))
	out := h
	for _, r := range recs {
		b := make([]byte, 48)
		copy(b[0:], net.ParseIP(r[0].(string)).To4())
		copy(b[4:], net.ParseIP(r[1].(string)).To4())
		binary.BigEndian.PutUint32(b[16:], 3)
		binary.BigEndian.PutUint32(b[20:], uint32(r[2].(int)))
		binary.BigEndian.PutUint16(b[34:], 443)
		b[38] = 6
		out = append(out, b...)
	}
	return out
}

func TestFlowsEndToEnd(t *testing.T) {
	s, _, do := scaleServer(t)
	var tid int64
	s.DB.QueryRow(`SELECT id FROM tenants WHERE name = 'default'`).Scan(&tid)
	var aid int64
	if err := s.DB.QueryRow(`INSERT INTO assets (tenant_id, name, kind, language, collection, status) VALUES ($1,'web-1','host','none','agent','up') RETURNING id`, tid).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(`INSERT INTO host_addrs (tenant_id, asset_id, ip) VALUES ($1,$2,'10.0.0.1')`, tid, aid)

	s.initFlows(FlowConfig{Tenant: tid, Bucket: time.Minute, TopN: 2, Retention: 1, Allow: []*net.IPNet{{IP: net.IPv4(192, 0, 2, 0), Mask: net.CIDRMask(24, 32)}}})
	// izinli olmayan ihracatçı reddedilir
	s.handleFlowPacket(net.ParseIP("203.0.113.9"), v5Pkt([3]any{"10.0.0.1", "8.8.8.8", 100}))
	if s.flows.denied.Load() != 1 || len(s.flows.agg) != 0 {
		t.Fatalf("izin listesi: denied=%d agg=%d", s.flows.denied.Load(), len(s.flows.agg))
	}
	exp := net.ParseIP("192.0.2.1")
	s.handleFlowPacket(exp, v5Pkt([3]any{"10.0.0.1", "8.8.8.8", 9000}, [3]any{"10.0.0.1", "8.8.4.4", 5000}, [3]any{"10.0.0.7", "1.1.1.1", 100}, [3]any{"10.0.0.8", "1.1.1.1", 50}))
	s.handleFlowPacket(exp, v5Pkt([3]any{"10.0.0.1", "8.8.8.8", 1000}))
	s.flushFlows(t.Context(), true)

	var n int
	s.DB.QueryRow(`SELECT count(*) FROM flow_agg`).Scan(&n)
	if n != 3 { // üst 2 + "diğer"
		t.Fatalf("flow_agg satırı = %d, beklenen 3", n)
	}
	var b int64
	s.DB.QueryRow(`SELECT bytes FROM flow_agg WHERE src = '10.0.0.1' AND dst = '8.8.8.8'`).Scan(&b)
	if b != 10000 {
		t.Errorf("birleştirme: %d", b)
	}
	var other int64
	s.DB.QueryRow(`SELECT bytes FROM flow_agg WHERE src = 'diğer'`).Scan(&other)
	if other != 150 {
		t.Errorf("diğer = %d", other)
	}
	var sa int64
	s.DB.QueryRow(`SELECT src_asset FROM flow_agg WHERE dst = '8.8.8.8'`).Scan(&sa)
	if sa != aid {
		t.Errorf("IP→asset eşleşmedi: %d", sa)
	}
	var bps float64
	if err := s.DB.QueryRow(`SELECT value FROM metrics WHERE asset_id = $1 AND name = 'flow.tx_bps'`, aid).Scan(&bps); err != nil || bps < 1 {
		t.Errorf("flow.tx_bps: %v %v", bps, err)
	}

	c, body, _ := do("GET", "/api/flows/summary?since=1h", nil)
	var sum struct {
		Enabled       bool  `json:"enabled"`
		Total         int64 `json:"total_bytes"`
		Conversations []struct {
			Src      string `json:"src"`
			SrcAsset string `json:"src_asset"`
			Bytes    int64  `json:"bytes"`
			Proto    string `json:"proto"`
		}
		Talkers []struct {
			Label string
			Bytes int64
		}
		Collector map[string]any
	}
	json.Unmarshal(body, &sum)
	if c != 200 || !sum.Enabled || sum.Total != 15150 || len(sum.Conversations) != 2 || sum.Conversations[0].SrcAsset != "web-1" || sum.Conversations[0].Proto != "TCP" {
		t.Fatalf("özet: %d %s", c, body)
	}
	if len(sum.Talkers) == 0 || sum.Talkers[0].Label != "10.0.0.1" || sum.Collector == nil {
		t.Errorf("talkers/collector: %+v", sum)
	}
	if c, _, _ = do("GET", "/api/flows/summary?asset=zzz", nil); c != 400 {
		t.Errorf("geçersiz asset: %d", c)
	}

	s.DB.Exec(`UPDATE flow_agg SET bucket = now() - interval '3 days'`)
	s.purgeFlows(t.Context())
	s.DB.QueryRow(`SELECT count(*) FROM flow_agg`).Scan(&n)
	if n != 0 {
		t.Errorf("saklama temizliği: %d", n)
	}
}
