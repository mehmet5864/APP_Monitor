package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"appmon/internal/netflow"
)

// ---- NetFlow / IPFIX toplayıcı (v3.11.0) ----
// UDP'de NetFlow v5/v9 ve IPFIX alır, bellekte aralık (varsayılan 5 dk) özetlerine indirger, her aralıkta en çok N konuşmayı
// (kalanı "diğer") flow_agg tablosuna yazar. Ham akış saklanmaz. Adresler asset'lere (ajanın bildirdiği adresler ve probe
// hedefleri) eşlenir; eşleşen asset'ler için flow.rx_bps / flow.tx_bps metrikleri yazılır.

type FlowConfig struct {
	Addr      string
	Allow     []*net.IPNet
	Tenant    int64
	Bucket    time.Duration // özet aralığı (varsayılan 5 dk)
	TopN      int           // aralık ve ihracatçı başına saklanan konuşma (varsayılan 500)
	Retention int           // gün (varsayılan 7)
	Sampling  uint32        // ihracatçı örnekleme bildirmiyorsa çarpan (varsayılan 1)
}

type flowKey struct {
	exp, src, dst string
	port          uint16
	proto         uint8
}
type flowVal struct{ bytes, packets, flows uint64 }

type flowState struct {
	mu      sync.Mutex
	cfg     FlowConfig
	dec     *netflow.Decoder
	agg     map[flowKey]*flowVal
	bucket  time.Time
	exps    map[string]time.Time
	dropped atomic.Uint64
	denied  atomic.Uint64
	lastAt  atomic.Int64
	ipmu    sync.Mutex
	ipmap   map[string]int64
	ipAt    time.Time
}

const maxFlowKeys = 300000

func (c FlowConfig) bucket() time.Duration {
	if c.Bucket >= time.Minute {
		return c.Bucket
	}
	return 5 * time.Minute
}
func (c FlowConfig) topN() int {
	if c.TopN > 0 {
		return c.TopN
	}
	return 500
}
func (c FlowConfig) retention() int {
	if c.Retention > 0 {
		return c.Retention
	}
	return 7
}

func (s *Server) initFlows(cfg FlowConfig) {
	s.flows = &flowState{cfg: cfg, dec: netflow.NewDecoder(), agg: map[flowKey]*flowVal{}, exps: map[string]time.Time{}}
	s.FlowsOn = true
}

// StartFlows UDP toplayıcıyı başlatır; ctx bitince kapanır.
func (s *Server) StartFlows(ctx context.Context, cfg FlowConfig) error {
	pc, err := net.ListenPacket("udp", cfg.Addr)
	if err != nil {
		return err
	}
	s.initFlows(cfg)
	go func() { <-ctx.Done(); pc.Close() }()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, src, err := pc.ReadFrom(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			ip := addrIP(src)
			s.handleFlowPacket(ip, buf[:n])
		}
	}()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				s.flushFlows(context.Background(), true)
				return
			case <-t.C:
				s.flushFlows(ctx, false)
			}
		}
	}()
	log.Printf("NetFlow/IPFIX toplayıcı dinliyor: %s (aralık %s, en çok %d konuşma)", pc.LocalAddr(), cfg.bucket(), cfg.topN())
	return nil
}

func (s *Server) handleFlowPacket(src net.IP, pkt []byte) {
	st := s.flows
	if st == nil {
		return
	}
	if len(st.cfg.Allow) > 0 {
		ok := false
		for _, n := range st.cfg.Allow {
			if src != nil && n.Contains(src) {
				ok = true
				break
			}
		}
		if !ok {
			st.denied.Add(1)
			return
		}
	}
	exp := ""
	if src != nil {
		exp = src.String()
	}
	fl, _ := st.dec.Decode(exp, pkt)
	if len(fl) == 0 {
		return
	}
	now := time.Now()
	st.lastAt.Store(now.Unix())
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.exps) < 1000 {
		st.exps[exp] = now
	} else if _, ok := st.exps[exp]; !ok {
		st.dropped.Add(uint64(len(fl)))
		return
	} else {
		st.exps[exp] = now
	}
	for _, f := range fl {
		mult := uint64(1)
		if f.Sampling > 1 {
			mult = uint64(f.Sampling)
		} else if st.cfg.Sampling > 1 {
			mult = uint64(st.cfg.Sampling)
		}
		k := flowKey{exp, f.Src.String(), f.Dst.String(), f.DstPort, f.Proto}
		v := st.agg[k]
		if v == nil {
			if len(st.agg) >= maxFlowKeys {
				st.dropped.Add(1)
				continue
			}
			v = &flowVal{}
			st.agg[k] = v
		}
		v.bytes += f.Bytes * mult
		v.packets += f.Packets * mult
		v.flows++
	}
}

// ipAssets IP → asset kimliği eşlemesi (ajanın bildirdiği adresler ve IP olan probe hedefleri); 60 sn önbellek.
func (s *Server) ipAssets(ctx context.Context, tenant int64) map[string]int64 {
	st := s.flows
	st.ipmu.Lock()
	defer st.ipmu.Unlock()
	if st.ipmap != nil && time.Since(st.ipAt) < 60*time.Second {
		return st.ipmap
	}
	m := map[string]int64{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT ip, asset_id FROM host_addrs WHERE tenant_id = $1`, tenant); err == nil {
		for rows.Next() {
			var ip string
			var id int64
			if rows.Scan(&ip, &id) == nil {
				m[ip] = id
			}
		}
		rows.Close()
	}
	if rows, err := s.DB.QueryContext(ctx, `SELECT target, asset_id FROM probes WHERE tenant_id = $1 AND type IN ('icmp','host','snmp','snmpx','tcp')`, tenant); err == nil {
		for rows.Next() {
			var tg string
			var id int64
			if rows.Scan(&tg, &id) == nil {
				if h, _, err := net.SplitHostPort(tg); err == nil {
					tg = h
				}
				if net.ParseIP(tg) != nil {
					if _, ok := m[tg]; !ok {
						m[tg] = id
					}
				}
			}
		}
		rows.Close()
	}
	st.ipmap, st.ipAt = m, time.Now()
	return m
}

// flushFlows aralık dolduysa (veya force ise) özetleri yazar.
func (s *Server) flushFlows(ctx context.Context, force bool) {
	st := s.flows
	if st == nil {
		return
	}
	bk := st.cfg.bucket()
	now := time.Now()
	cur := now.Truncate(bk)
	st.mu.Lock()
	if st.bucket.IsZero() {
		st.bucket = cur
	}
	if !force && !cur.After(st.bucket) {
		st.mu.Unlock()
		return
	}
	agg, bucket := st.agg, st.bucket
	st.agg, st.bucket = map[flowKey]*flowVal{}, cur
	st.mu.Unlock()
	if len(agg) == 0 {
		return
	}
	if err := s.writeFlows(ctx, bucket, bk, agg); err != nil {
		log.Printf("flows/yaz: %v", err)
	}
}

type flowRow struct {
	k flowKey
	v *flowVal
}

func (s *Server) writeFlows(ctx context.Context, bucket time.Time, bk time.Duration, agg map[flowKey]*flowVal) error {
	st := s.flows
	ipm := s.ipAssets(ctx, st.cfg.Tenant)
	byExp := map[string][]flowRow{}
	for k, v := range agg {
		byExp[k.exp] = append(byExp[k.exp], flowRow{k, v})
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rx, tx2 := map[int64]uint64{}, map[int64]uint64{}
	topN := st.cfg.topN()
	insert := func(r flowRow, other bool) error {
		var sa, da any
		if id, ok := ipm[r.k.src]; ok {
			sa = id
		}
		if id, ok := ipm[r.k.dst]; ok {
			da = id
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO flow_agg (tenant_id, bucket, exporter, src, dst, dst_port, proto, bytes, packets, flows, src_asset, dst_asset)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, st.cfg.Tenant, bucket, r.k.exp, r.k.src, r.k.dst, int(r.k.port), int(r.k.proto), int64(r.v.bytes), int64(r.v.packets), int(r.v.flows), sa, da)
		return err
	}
	for exp, rows := range byExp {
		sort.Slice(rows, func(i, j int) bool { return rows[i].v.bytes > rows[j].v.bytes })
		var other flowVal
		for i, r := range rows {
			if i < topN {
				if err := insert(r, false); err != nil {
					tx.Rollback()
					return err
				}
			} else {
				other.bytes += r.v.bytes
				other.packets += r.v.packets
				other.flows += r.v.flows
			}
		}
		if other.flows > 0 {
			if err := insert(flowRow{flowKey{exp: exp, src: "diğer", dst: "diğer"}, &other}, true); err != nil {
				tx.Rollback()
				return err
			}
		}
		for _, r := range rows { // asset metrikleri tüm konuşmalardan (yalnızca üst N değil)
			if id, ok := ipm[r.k.src]; ok {
				tx2[id] += r.v.bytes
			}
			if id, ok := ipm[r.k.dst]; ok {
				rx[id] += r.v.bytes
			}
		}
	}
	secs := bk.Seconds()
	n := 0
	for id, b := range tx2 {
		if n++; n > 2000 {
			break
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES (now(),$1,$2,'flow.tx_bps',$3)`, st.cfg.Tenant, id, float64(b)*8/secs); err != nil {
			tx.Rollback()
			return err
		}
	}
	n = 0
	for id, b := range rx {
		if n++; n > 2000 {
			break
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES (now(),$1,$2,'flow.rx_bps',$3)`, st.cfg.Tenant, id, float64(b)*8/secs); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) purgeFlows(ctx context.Context) {
	days := 7
	if s.flows != nil {
		days = s.flows.cfg.retention()
	}
	s.DB.ExecContext(ctx, `DELETE FROM flow_agg WHERE bucket < now() - make_interval(days => $1)`, days)
}

var protoNames = map[int]string{1: "ICMP", 6: "TCP", 17: "UDP", 47: "GRE", 50: "ESP", 58: "ICMPv6"}

func protoName(p int) string {
	if n, ok := protoNames[p]; ok {
		return n
	}
	return strconv.Itoa(p)
}

// GET /api/flows/summary?since=1h|6h|24h|7d&asset=ID
func (s *Server) flowSummary(w http.ResponseWriter, r *http.Request, a Auth) {
	win := traceWindow(r)
	args := []any{a.TenantID, win.Seconds()}
	cond := "tenant_id = $1 AND bucket > now() - make_interval(secs => $2)"
	if v := r.URL.Query().Get("asset"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			fail(w, 400, "geçersiz asset")
			return
		}
		args = append(args, id)
		cond += " AND (src_asset = $3 OR dst_asset = $3)"
	}
	type pair struct {
		Label string `json:"label"`
		Bytes int64  `json:"bytes"`
	}
	top := func(expr string, n int) []pair {
		out := []pair{}
		rows, err := s.DB.QueryContext(r.Context(), `SELECT `+expr+` AS k, sum(bytes) b FROM flow_agg WHERE `+cond+` AND src <> 'diğer' GROUP BY k ORDER BY b DESC LIMIT `+strconv.Itoa(n), args...)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var p pair
			if rows.Scan(&p.Label, &p.Bytes) == nil {
				out = append(out, p)
			}
		}
		return out
	}
	var total, pk, fl int64
	s.DB.QueryRowContext(r.Context(), `SELECT COALESCE(sum(bytes),0), COALESCE(sum(packets),0), COALESCE(sum(flows),0) FROM flow_agg WHERE `+cond, args...).Scan(&total, &pk, &fl)
	convs := []map[string]any{}
	if rows, err := s.DB.QueryContext(r.Context(),
		`SELECT f.src, f.dst, f.dst_port, f.proto, sum(f.bytes) b, sum(f.packets) p, COALESCE(max(sa.name),''), COALESCE(max(da.name),'')
		   FROM flow_agg f LEFT JOIN assets sa ON sa.id = f.src_asset LEFT JOIN assets da ON da.id = f.dst_asset
		  WHERE f.`+strings.ReplaceAll(cond, " AND ", " AND f.")+` AND f.src <> 'diğer' GROUP BY f.src, f.dst, f.dst_port, f.proto ORDER BY b DESC LIMIT 25`, args...); err == nil {
		for rows.Next() {
			var src, dst, sn, dn string
			var port, proto int
			var b, p int64
			if rows.Scan(&src, &dst, &port, &proto, &b, &p, &sn, &dn) == nil {
				convs = append(convs, map[string]any{"src": src, "dst": dst, "src_asset": sn, "dst_asset": dn, "port": port, "proto": protoName(proto), "bytes": b, "packets": p})
			}
		}
		rows.Close()
	}
	ports := []map[string]any{}
	if rows, err := s.DB.QueryContext(r.Context(), `SELECT dst_port, proto, sum(bytes) b FROM flow_agg WHERE `+cond+` AND src <> 'diğer' GROUP BY dst_port, proto ORDER BY b DESC LIMIT 10`, args...); err == nil {
		for rows.Next() {
			var port, proto int
			var b int64
			if rows.Scan(&port, &proto, &b) == nil {
				ports = append(ports, map[string]any{"port": port, "proto": protoName(proto), "bytes": b})
			}
		}
		rows.Close()
	}
	series := []map[string]any{}
	if rows, err := s.DB.QueryContext(r.Context(), `SELECT bucket, sum(bytes) FROM flow_agg WHERE `+cond+` GROUP BY bucket ORDER BY bucket`, args...); err == nil {
		for rows.Next() {
			var t time.Time
			var b int64
			if rows.Scan(&t, &b) == nil {
				series = append(series, map[string]any{"t": t, "bytes": b})
			}
		}
		rows.Close()
	}
	out := map[string]any{"enabled": s.FlowsOn, "total_bytes": total, "total_packets": pk, "flows": fl,
		"talkers": top("src", 10), "receivers": top("dst", 10), "protocols": func() []pair {
			o := []pair{}
			for _, p := range top("proto::text", 8) {
				n, _ := strconv.Atoi(p.Label)
				o = append(o, pair{protoName(n), p.Bytes})
			}
			return o
		}(), "ports": ports, "conversations": convs, "series": series}
	if s.flows != nil {
		st := s.flows
		st.mu.Lock()
		exps := len(st.exps)
		keys := len(st.agg)
		st.mu.Unlock()
		out["collector"] = map[string]any{"exporters": exps, "packets": st.dec.Packets.Load(), "flows": st.dec.Flows.Load(), "invalid": st.dec.Invalid.Load(),
			"missing_template": st.dec.MissingTemplate.Load(), "denied": st.denied.Load(), "dropped": st.dropped.Load(), "pending": keys,
			"last": st.lastAt.Load(), "bucket_sec": int(st.cfg.bucket().Seconds()), "retention_days": st.cfg.retention(), "top_n": st.cfg.topN()}
	}
	writeJSON(w, 200, out)
}
