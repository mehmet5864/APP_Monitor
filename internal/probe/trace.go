package probe

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Hop yol üzerindeki bir sıçramadır. IP boşsa o sıçrama yanıt vermemiştir (güvenlik duvarı veya ICMP kapalı).
type Hop struct {
	TTL     int       `json:"ttl"`
	IP      string    `json:"ip,omitempty"`
	Name    string    `json:"name,omitempty"`
	RTTms   float64   `json:"rtt_ms"`
	LossPct float64   `json:"loss_pct"`
	Jitter  float64   `json:"jitter_ms"`         // örneklerin standart sapması (ms)
	DSCPIn  *int      `json:"dscp_in,omitempty"` // bu sıçramaya varırken paketteki DSCP (yönlendiricinin geri alıntıladığı başlıktan); bilinmiyorsa yok
	Asset   string    `json:"asset,omitempty"`   // IP APP_MON'daki bir asset'e aitse adı (uygulama katmanı doldurur)
	Reached bool      `json:"reached,omitempty"`
	Samples []float64 `json:"-"`
}

// TraceResult bir yol izleme çıktısıdır.
type TraceResult struct {
	Target  string `json:"target"`
	DestIP  string `json:"dest_ip"`
	Hops    []Hop  `json:"hops"`
	Reached bool   `json:"reached"`
	Route   string `json:"route"` // yanıt veren sıçramaların özeti (rota değişimi tespiti için)
	DSCP    int    `json:"dscp"`  // gönderilen DSCP işareti
	// MarkChangedTTL işaretin ilk değiştiği sıçramadır (0 = değişmedi veya ölçülemedi); MarkSeen DSCP'si okunabilen sıçrama sayısıdır.
	MarkChangedTTL int `json:"mark_changed_ttl"`
	MarkSeen       int `json:"mark_seen"`
}

// markChange ilk işaret değişimini ve DSCP'si okunabilen sıçrama sayısını bulur.
func markChange(h []Hop, sent int) (ttl, seen int) {
	for _, x := range h {
		if x.DSCPIn == nil {
			continue
		}
		seen++
		if ttl == 0 && *x.DSCPIn != sent {
			ttl = x.TTL
		}
	}
	return ttl, seen
}

// parseTraceReply bir ICMP mesajından (IP başlığı olmadan) eşleşen id/seq'i ve türünü çıkarır.
// kind: "reply" (echo reply), "exceeded" (TTL doldu), "unreach" (ulaşılamıyor), "" (ilgisiz).
// tos: hata mesajının geri alıntıladığı özgün IP başlığındaki ToS baytı (DSCP<<2); alıntı yoksa -1.
func parseTraceReply(b []byte, id uint16) (kind string, seq uint16, tos int) {
	if len(b) < 8 {
		return "", 0, -1
	}
	switch b[0] {
	case 0:
		if binary.BigEndian.Uint16(b[4:]) == id {
			return "reply", binary.BigEndian.Uint16(b[6:]), -1
		}
	case 11, 3:
		in := b[8:]
		if len(in) < 28 || in[0]>>4 != 4 {
			return "", 0, -1
		}
		ihl := int(in[0]&0xf) * 4
		if len(in) < ihl+8 || in[9] != 1 || in[ihl] != 8 {
			return "", 0, -1
		}
		if binary.BigEndian.Uint16(in[ihl+4:]) != id {
			return "", 0, -1
		}
		k := "exceeded"
		if b[0] == 3 {
			k = "unreach"
		}
		return k, binary.BigEndian.Uint16(in[ihl+6:]), int(in[1])
	}
	return "", 0, -1
}

func routeSummary(h []Hop) string {
	var ips []string
	for _, x := range h {
		if x.IP != "" {
			ips = append(ips, x.IP)
		}
	}
	return strings.Join(ips, ">")
}

// Trace hedefe giden IPv4 yolunu ICMP echo + artan TTL ile çıkarır (traceroute/MTR benzeri).
// Ham ICMP soketi gerekir (CAP_NET_RAW veya yönetici).
func Trace(ctx context.Context, target string, maxHops, queries int, wait time.Duration, block bool, dscp int) (TraceResult, error) {
	if dscp < 0 || dscp > 63 {
		dscp = 0
	}
	res := TraceResult{Target: target, DSCP: dscp}
	ip, err := resolveIP(ctx, target, block)
	if err != nil {
		return res, err
	}
	if ip.To4() == nil {
		return res, errors.New("yol izleme şimdilik yalnızca IPv4 hedefleri destekler")
	}
	res.DestIP = ip.String()
	if maxHops < 1 || maxHops > 40 {
		maxHops = 30
	}
	if queries < 1 || queries > 5 {
		queries = 3
	}
	if wait <= 0 {
		wait = time.Second
	}
	pc, err := traceConn()
	if err != nil {
		return res, fmt.Errorf("ham ICMP soketi açılamadı (%v): CAP_NET_RAW yetkisi gerekir", err)
	}
	defer pc.Close()
	if dscp > 0 {
		if err := setTOS(pc, dscp<<2); err != nil {
			return res, fmt.Errorf("DSCP işareti yazılamadı: %v", err)
		}
	}
	id := uint16(os.Getpid()&0xffff) ^ 0x5a5a
	buf := make([]byte, 1500)
	dst := &net.IPAddr{IP: ip}
	for ttl := 1; ttl <= maxHops && ctx.Err() == nil; ttl++ {
		if err := setTTL(pc, ttl); err != nil {
			return res, err
		}
		hop := Hop{TTL: ttl}
		lost := 0
		for q := 0; q < queries; q++ {
			seq := uint16(ttl*8 + q)
			msg := make([]byte, 8+16)
			msg[0] = 8
			binary.BigEndian.PutUint16(msg[4:], id)
			binary.BigEndian.PutUint16(msg[6:], seq)
			copy(msg[8:], "appmon-path-trac")
			binary.BigEndian.PutUint16(msg[2:], icmpChecksum(msg))
			sent := time.Now()
			if _, err := pc.WriteTo(msg, dst); err != nil {
				return res, err
			}
			pc.SetReadDeadline(sent.Add(wait))
			got := false
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					break
				}
				kind, rs, tos := parseTraceReply(buf[:n], id)
				if kind == "" || rs != seq {
					continue
				}
				if tos >= 0 && hop.DSCPIn == nil {
					d := tos >> 2
					hop.DSCPIn = &d
				}
				got = true
				if fa, ok := from.(*net.IPAddr); ok {
					hop.IP = fa.IP.String()
				}
				hop.Samples = append(hop.Samples, float64(time.Since(sent).Microseconds())/1000)
				if kind == "reply" || kind == "unreach" {
					hop.Reached = true
				}
				break
			}
			if !got {
				lost++
			}
		}
		hop.LossPct = float64(lost) * 100 / float64(queries)
		if len(hop.Samples) > 0 {
			var sum float64
			for _, v := range hop.Samples {
				sum += v
			}
			hop.RTTms = sum / float64(len(hop.Samples))
			if len(hop.Samples) > 1 {
				var sq float64
				for _, v := range hop.Samples {
					sq += (v - hop.RTTms) * (v - hop.RTTms)
				}
				hop.Jitter = math.Sqrt(sq / float64(len(hop.Samples)))
			}
		}
		if hop.IP != "" {
			if names, err := net.DefaultResolver.LookupAddr(ctx, hop.IP); err == nil && len(names) > 0 {
				hop.Name = strings.TrimSuffix(names[0], ".")
			}
		}
		res.Hops = append(res.Hops, hop)
		if hop.Reached {
			res.Reached = true
			break
		}
	}
	res.Route = routeSummary(res.Hops)
	res.MarkChangedTTL, res.MarkSeen = markChange(res.Hops, dscp)
	return res, nil
}

var routeStore = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// routeChanged hedef için önceki rota özetiyle karşılaştırır ve yenisini saklar. İlk ölçümde false döner.
func routeChanged(key, route string) bool {
	sum := sha1.Sum([]byte(route))
	h := hex.EncodeToString(sum[:])
	routeStore.Lock()
	defer routeStore.Unlock()
	old, ok := routeStore.m[key]
	routeStore.m[key] = h
	return ok && old != h
}

// pathProbe "trace" probe türüdür: hedefe yolu çıkarır, hop sayısı / son sıçrama gecikmesi / kayıp ve rota değişimi metrikleri üretir.
func pathProbe(ctx context.Context, spec Spec, block bool) (map[string]float64, error) {
	r, err := Trace(ctx, spec.Target, 30, 2, 800*time.Millisecond, block, spec.Opts.DSCP)
	if err != nil {
		return nil, err
	}
	spec.Det.Add("Hedef IP", r.DestIP)
	for _, h := range r.Hops {
		who := h.IP
		if h.Name != "" && h.Name != h.IP {
			who = h.Name + " (" + h.IP + ")"
		}
		if who == "" {
			who = "yanıt yok"
		}
		line := fmt.Sprintf("%s · %.1f ms · %%%.0f kayıp", who, h.RTTms, h.LossPct)
		if h.DSCPIn != nil {
			line += fmt.Sprintf(" · DSCP %d", *h.DSCPIn)
		}
		spec.Det.Add(fmt.Sprintf("Sıçrama %d", h.TTL), line)
	}
	x := map[string]float64{"net.path.hops": float64(len(r.Hops))}
	if n := len(r.Hops); n > 0 {
		last := r.Hops[n-1]
		x["net.path.rtt_ms"] = last.RTTms
		x["net.path.loss_pct"] = last.LossPct
	}
	if r.DSCP > 0 {
		spec.Det.Add("Gönderilen DSCP", r.DSCP)
		x["net.path.dscp_changed_ttl"] = float64(r.MarkChangedTTL)
	}
	if routeChanged(spec.Target, r.Route) {
		x["net.path.changed"] = 1
	} else {
		x["net.path.changed"] = 0
	}
	if !r.Reached {
		return x, errors.New("hedefe ulaşılamadı (yol " + fmt.Sprint(len(r.Hops)) + " sıçramada kesildi)")
	}
	return x, nil
}
