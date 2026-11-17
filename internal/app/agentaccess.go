package app

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"
)

// Ajanın okuduğu web sunucusu erişim günlüklerinin özetleri (v2.7.6). Ajan satırları kendisi çözer, uç nokta bazında sayar ve her
// gönderimde birkaç yüz özet satırı yollar; sunucu bunları syslog alıcısıyla aynı 5 dk'lık özetlere ekler (Performans → Uygulamalar).

// AgentAccess bir (uygulama, uç nokta) için ajanın özetidir.
type AgentAccess struct {
	App      string          `json:"app"`
	Endpoint string          `json:"endpoint"` // "GET /yol"
	Reqs     int64           `json:"reqs"`
	E4       int64           `json:"e4"`
	E5       int64           `json:"e5"`
	Untimed  int64           `json:"untimed"` // istek süresi olmayan istekler (reqs içinde)
	SumMS    float64         `json:"sum_ms"`
	MaxMS    float64         `json:"max_ms"`
	B        [nBuckets]int64 `json:"b"`
}

const maxAccessEntries = 800

func okCount(n int64) bool { return n >= 0 && n <= 1_000_000_000 }

func okFloat(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= 1e15 }

// mergeAgentAccess özetleri doğrulayıp bellekteki 5 dk'lık toplamlara ekler; kabul edilen satır sayısını verir.
func (s *Server) mergeAgentAccess(tenant int64, list []AgentAccess) int {
	if len(list) == 0 {
		return 0
	}
	if len(list) > maxAccessEntries {
		list = list[:maxAccessEntries]
	}
	st := s.InitAccess(context.Background())
	stat := s.syslogStats()
	now := time.Now()
	accepted := 0
	st.mu.Lock()
	defer st.mu.Unlock()
	if d := now.UTC().Format("2006-01-02"); d != st.day {
		st.day, st.endpoints = d, map[string]map[string]bool{}
	}
	for _, e := range list {
		if !appNameRe.MatchString(e.App) || !okCount(e.Reqs) || !okCount(e.E4) || !okCount(e.E5) || !okCount(e.Untimed) ||
			!okFloat(e.SumMS) || !okFloat(e.MaxMS) || e.Reqs == 0 || e.Untimed > e.Reqs || e.E4+e.E5 > e.Reqs {
			stat.Invalid.Add(1)
			continue
		}
		var bsum int64
		bad := false
		for _, c := range e.B {
			if !okCount(c) {
				bad = true
			}
			bsum += c
		}
		if bad || bsum > e.Reqs-e.Untimed {
			stat.Invalid.Add(1)
			continue
		}
		// Uç nokta: "METOD /yol"; yol sunucuda da normalleştirilir (ajana güvenilmez).
		method, path, _ := strings.Cut(e.Endpoint, " ")
		if !methodRe.MatchString(method) {
			method = "OTHER"
		}
		ep := method + " " + NormalizePath(path)
		if e.Endpoint == "OTHER /*" {
			ep = "OTHER /*"
		}
		if st.apps[tenant] == nil {
			st.apps[tenant] = map[string]bool{}
		}
		if !st.apps[tenant][e.App] {
			if len(st.apps[tenant]) >= maxAppsPerTenant {
				stat.Dropped.Add(e.Reqs)
				continue
			}
			st.apps[tenant][e.App] = true
		}
		ek := strconv.FormatInt(tenant, 10) + "|" + e.App
		set := st.endpoints[ek]
		if set == nil {
			set = map[string]bool{}
			st.endpoints[ek] = set
		}
		if !set[ep] {
			if len(set) >= maxEndpointsPerApp {
				ep = "OTHER /*"
			} else {
				set[ep] = true
			}
		}
		k := aggKey{tenant, e.App, now.Truncate(statBucket), ep}
		a := st.agg[k]
		if a == nil {
			if len(st.agg) > 200000 {
				stat.Dropped.Add(e.Reqs)
				continue
			}
			a = &epAgg{}
			st.agg[k] = a
		}
		a.reqs += e.Reqs
		a.e4 += e.E4
		a.e5 += e.E5
		a.untimed += e.Untimed
		a.sum += e.SumMS
		if e.MaxMS > a.max {
			a.max = e.MaxMS
		}
		for i, c := range e.B {
			a.b[i] += c
		}
		stat.Accepted.Add(e.Reqs)
		accepted++
	}
	if accepted > 0 {
		stat.LastAt.Store(now.Unix())
	}
	return accepted
}
