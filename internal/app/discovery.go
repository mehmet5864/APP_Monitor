package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"appmon/internal/probe"
)

var defaultScanPorts = []int{22, 80, 135, 139, 443, 445, 1433, 3306, 3389, 5432, 8080, 8443}

// discoveredHost keşif sonucunda bulunan bir makinedir.
type discoveredHost struct {
	IP    string  `json:"ip"`
	Name  string  `json:"name,omitempty"`
	Ports []int   `json:"ports"`
	Ping  bool    `json:"ping"`
	RTTms float64 `json:"rtt_ms,omitempty"`
}

type discoveryJob struct {
	ID      string           `json:"id"`
	Tenant  int64            `json:"-"`
	CIDR    string           `json:"cidr"`
	State   string           `json:"state"` // running | done | cancelled
	Total   int              `json:"total"`
	Done    int              `json:"done"`
	Hosts   []discoveredHost `json:"hosts"`
	Started time.Time        `json:"started"`
	cancel  context.CancelFunc
}

var discovery = struct {
	sync.Mutex
	jobs map[string]*discoveryJob
	seq  int
}{jobs: map[string]*discoveryJob{}}

// expandCIDR en çok 1024 adreslik (/22) IPv4 aralığındaki makine adreslerini döndürür.
func expandCIDR(c string) ([]net.IP, error) {
	_, n, err := net.ParseCIDR(c)
	if err != nil || n.IP.To4() == nil {
		return nil, fmt.Errorf("geçerli bir IPv4 CIDR girin (ör. 192.168.1.0/24)")
	}
	ones, bits := n.Mask.Size()
	if ones < 22 {
		return nil, fmt.Errorf("en çok /22 (1024 adres) taranabilir")
	}
	base := n.IP.To4()
	total := 1 << (bits - ones)
	var out []net.IP
	for i := 0; i < total; i++ {
		ip := make(net.IP, 4)
		v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		v += uint32(i)
		ip[0], ip[1], ip[2], ip[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
		if total > 2 && (i == 0 || i == total-1) { // ağ ve yayın adresi
			continue
		}
		out = append(out, ip)
	}
	return out, nil
}

func scanHost(ctx context.Context, ip net.IP, ports []int) (discoveredHost, bool) {
	h := discoveredHost{IP: ip.String(), Ports: []int{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := probe.RunX(ctx, probe.Spec{Type: "icmp", Target: h.IP, Timeout: 900 * time.Millisecond, Opts: probe.Options{Count: 1}}, false)
		if r.Up {
			mu.Lock()
			h.Ping, h.RTTms = true, float64(r.Latency.Microseconds())/1000
			mu.Unlock()
		}
	}()
	for _, p := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			d := net.Dialer{Timeout: 700 * time.Millisecond}
			c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(h.IP, fmt.Sprint(p)))
			if err == nil {
				c.Close()
				mu.Lock()
				h.Ports = append(h.Ports, p)
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	sort.Ints(h.Ports)
	if !h.Ping && len(h.Ports) == 0 {
		return h, false
	}
	if names, err := net.DefaultResolver.LookupAddr(ctx, h.IP); err == nil && len(names) > 0 {
		h.Name = names[0]
		for len(h.Name) > 0 && h.Name[len(h.Name)-1] == '.' {
			h.Name = h.Name[:len(h.Name)-1]
		}
	}
	return h, true
}

// startDiscovery POST /api/discovery {cidr, ports}: arka planda tarar; GET /api/discovery/{id} ile izlenir.
func (s *Server) startDiscovery(w http.ResponseWriter, r *http.Request, a Auth) {
	if s.Mode == "saas" {
		fail(w, 403, "SaaS modunda ağ keşfi kapalıdır")
		return
	}
	var req struct {
		CIDR  string `json:"cidr"`
		Ports []int  `json:"ports"`
	}
	if !decode(w, r, &req) {
		return
	}
	ips, err := expandCIDR(req.CIDR)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	ports := req.Ports
	if len(ports) == 0 {
		ports = defaultScanPorts
	}
	if len(ports) > 20 {
		fail(w, 400, "en çok 20 port taranabilir")
		return
	}
	for _, p := range ports {
		if p < 1 || p > 65535 {
			fail(w, 400, "port 1-65535 arasında olmalı")
			return
		}
	}
	discovery.Lock()
	for _, j := range discovery.jobs {
		if j.State == "running" {
			discovery.Unlock()
			fail(w, 409, "devam eden bir keşif var; bitmesini bekleyin veya iptal edin")
			return
		}
	}
	discovery.seq++
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	job := &discoveryJob{ID: fmt.Sprintf("k%d", discovery.seq), Tenant: a.TenantID, CIDR: req.CIDR, State: "running", Total: len(ips), Hosts: []discoveredHost{}, Started: time.Now(), cancel: cancel}
	discovery.jobs[job.ID] = job
	for id, j := range discovery.jobs { // en çok 5 iş sakla
		if len(discovery.jobs) > 5 && j.State != "running" && id != job.ID {
			delete(discovery.jobs, id)
		}
	}
	discovery.Unlock()
	s.audit(r.Context(), a.TenantID, a.Label, "discovery.start", req.CIDR)
	go func() {
		defer cancel()
		sem := make(chan struct{}, 48)
		var wg sync.WaitGroup
		for _, ip := range ips {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()
				h, ok := scanHost(ctx, ip, ports)
				discovery.Lock()
				job.Done++
				if ok {
					job.Hosts = append(job.Hosts, h)
				}
				discovery.Unlock()
			}(ip)
		}
		wg.Wait()
		discovery.Lock()
		if ctx.Err() != nil {
			job.State = "cancelled"
		} else {
			job.State = "done"
		}
		sort.Slice(job.Hosts, func(i, j int) bool {
			return net.ParseIP(job.Hosts[i].IP).To4().String() < net.ParseIP(job.Hosts[j].IP).To4().String()
		})
		discovery.Unlock()
	}()
	writeJSON(w, 202, map[string]any{"id": job.ID, "total": job.Total})
}

func (s *Server) getDiscovery(w http.ResponseWriter, r *http.Request, a Auth) {
	id := r.PathValue("id")
	discovery.Lock()
	defer discovery.Unlock()
	j := discovery.jobs[id]
	if j == nil || j.Tenant != a.TenantID {
		fail(w, 404, "keşif bulunamadı")
		return
	}
	cp := *j
	cp.Hosts = append([]discoveredHost{}, j.Hosts...)
	writeJSON(w, 200, cp)
}

func (s *Server) cancelDiscovery(w http.ResponseWriter, r *http.Request, a Auth) {
	discovery.Lock()
	j := discovery.jobs[r.PathValue("id")]
	discovery.Unlock()
	if j == nil || j.Tenant != a.TenantID {
		fail(w, 404, "keşif bulunamadı")
		return
	}
	j.cancel()
	w.WriteHeader(http.StatusNoContent)
}
