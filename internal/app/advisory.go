package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Zafiyet (CVE/GHSA) eşleme: OSV biçimli kayıtlar (https://osv.dev) bir dizinden (APM_ADVISORY_DIR) okunur. Dizine OSV'nin
// ekosistem arşivleri (npm, Packagist, PyPI, Maven, Go, NuGet "all.zip") veya tek tek .json kayıtlar konabilir; sunucu
// internete çıkabiliyorsa APM_ADVISORY_AUTO=1 ile arşivler günde bir indirilir. Eşleme paket adı + sürüm aralığıyla
// yapılır; bu bir tarayıcı değildir: yalnızca bilinen kayıtlarla karşılaştırır, sonuç "olası" kabul edilmelidir.

type advEvent struct{ Introduced, Fixed, LastAffected string }

type advEntry struct {
	ID       string
	Aliases  []string
	Summary  string
	Severity string // critical | high | medium | low | unknown
	Score    float64
	Ranges   [][]advEvent
	Versions []string
}

type advStore struct {
	mu       sync.RWMutex
	idx      map[string][]*advEntry
	records  int
	loadedAt time.Time
	sig      string
	loading  bool
	lastErr  string
}

var advEcos = map[string]bool{"npm": true, "Packagist": true, "PyPI": true, "Maven": true, "Go": true, "NuGet": true}

func advKey(eco, name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if eco == "PyPI" {
		n = strings.NewReplacer("_", "-", ".", "-").Replace(n)
	}
	return eco + "|" + n
}

type osvRec struct {
	ID               string                         `json:"id"`
	Aliases          []string                       `json:"aliases"`
	Summary          string                         `json:"summary"`
	Withdrawn        string                         `json:"withdrawn"`
	Severity         []struct{ Type, Score string } `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
	Affected []struct {
		Package struct{ Ecosystem, Name string } `json:"package"`
		Ranges  []struct {
			Type   string              `json:"type"`
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
		Versions []string `json:"versions"`
	} `json:"affected"`
}

func sevName(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return "critical"
	case "HIGH":
		return "high"
	case "MODERATE", "MEDIUM":
		return "medium"
	case "LOW":
		return "low"
	}
	return ""
}

func scoreSev(v float64) string {
	switch {
	case v >= 9:
		return "critical"
	case v >= 7:
		return "high"
	case v >= 4:
		return "medium"
	case v > 0:
		return "low"
	}
	return "unknown"
}

// cvss3 CVSS v3.x vektöründen temel puanı hesaplar (yoksa 0).
func cvss3(vec string) float64 {
	if !strings.HasPrefix(vec, "CVSS:3") {
		return 0
	}
	m := map[string]string{}
	for _, p := range strings.Split(vec, "/")[1:] {
		if kv := strings.SplitN(p, ":", 2); len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	av := map[string]float64{"N": .85, "A": .62, "L": .55, "P": .2}[m["AV"]]
	ac := map[string]float64{"L": .77, "H": .44}[m["AC"]]
	ui := map[string]float64{"N": .85, "R": .62}[m["UI"]]
	changed := m["S"] == "C"
	pr := map[string]float64{"N": .85, "L": .62, "H": .27}[m["PR"]]
	if changed {
		pr = map[string]float64{"N": .85, "L": .68, "H": .5}[m["PR"]]
	}
	cia := func(k string) float64 { return map[string]float64{"H": .56, "L": .22, "N": 0}[m[k]] }
	if av == 0 || ac == 0 || ui == 0 || pr == 0 {
		return 0
	}
	iss := 1 - (1-cia("C"))*(1-cia("I"))*(1-cia("A"))
	var imp float64
	if changed {
		imp = 7.52*(iss-.029) - 3.25*math.Pow(iss-.02, 15)
	} else {
		imp = 6.42 * iss
	}
	if imp <= 0 {
		return 0
	}
	ex := 8.22 * av * ac * pr * ui
	var base float64
	if changed {
		base = math.Min(1.08*(imp+ex), 10)
	} else {
		base = math.Min(imp+ex, 10)
	}
	return math.Ceil(base*10) / 10
}

func (st *advStore) add(b []byte) {
	var r osvRec
	if json.Unmarshal(b, &r) != nil || r.ID == "" || r.Withdrawn != "" {
		return
	}
	sev, score := sevName(r.DatabaseSpecific.Severity), 0.0
	for _, s := range r.Severity {
		if v := cvss3(s.Score); v > score {
			score = v
		}
	}
	if sev == "" {
		sev = scoreSev(score)
	}
	e := &advEntry{ID: r.ID, Aliases: r.Aliases, Summary: cleanText(r.Summary, 200), Severity: sev, Score: score}
	n := 0
	for _, a := range r.Affected {
		eco := strings.SplitN(a.Package.Ecosystem, ":", 2)[0]
		if !advEcos[eco] || a.Package.Name == "" {
			continue
		}
		ae := *e
		ae.Versions = a.Versions
		for _, rg := range a.Ranges {
			if rg.Type != "SEMVER" && rg.Type != "ECOSYSTEM" {
				continue
			}
			var evs []advEvent
			for _, ev := range rg.Events {
				evs = append(evs, advEvent{ev["introduced"], ev["fixed"], ev["last_affected"]})
			}
			ae.Ranges = append(ae.Ranges, evs)
		}
		if len(ae.Ranges) == 0 && len(ae.Versions) == 0 {
			continue
		}
		p := &ae
		st.idx[advKey(eco, a.Package.Name)] = append(st.idx[advKey(eco, a.Package.Name)], p)
		if eco == "Maven" {
			if i := strings.LastIndexByte(a.Package.Name, ':'); i >= 0 {
				k := advKey("Maven", a.Package.Name[i+1:])
				st.idx[k] = append(st.idx[k], p)
			}
		}
		n++
	}
	if n > 0 {
		st.records++
	}
}

// dirSig dizin içeriğinin (ad, boyut, zaman) kısa özetidir; değişince yeniden yüklenir.
func dirSig(dir string) string {
	var parts []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		n := strings.ToLower(d.Name())
		if strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".json") {
			if fi, e := d.Info(); e == nil {
				parts = append(parts, fmt.Sprintf("%s:%d:%d", p, fi.Size(), fi.ModTime().Unix()))
			}
		}
		return nil
	})
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func (s *Server) loadAdvisories() {
	dir := s.AdvisoryDir
	if dir == "" {
		return
	}
	sig := dirSig(dir)
	s.adv.mu.RLock()
	same, busy := sig == s.adv.sig && s.adv.idx != nil, s.adv.loading
	s.adv.mu.RUnlock()
	if same || busy {
		return
	}
	s.adv.mu.Lock()
	s.adv.loading = true
	s.adv.mu.Unlock()
	st := &advStore{idx: map[string][]*advEntry{}}
	files := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		n := strings.ToLower(d.Name())
		switch {
		case strings.HasSuffix(n, ".zip"):
			zr, err := zip.OpenReader(p)
			if err != nil {
				log.Printf("advisory: %s açılamadı: %v", p, err)
				return nil
			}
			files++
			for _, f := range zr.File {
				if !strings.HasSuffix(strings.ToLower(f.Name), ".json") || f.UncompressedSize64 > 4<<20 {
					continue
				}
				rc, err := f.Open()
				if err != nil {
					continue
				}
				b, _ := io.ReadAll(io.LimitReader(rc, 4<<20))
				rc.Close()
				st.add(b)
			}
			zr.Close()
		case strings.HasSuffix(n, ".json") && n != "eol.json":
			if b, err := os.ReadFile(p); err == nil && len(b) < 4<<20 {
				files++
				st.add(b)
			}
		}
		return nil
	})
	s.adv.mu.Lock()
	s.adv.idx, s.adv.records, s.adv.loadedAt, s.adv.sig, s.adv.loading = st.idx, st.records, time.Now(), sig, false
	s.adv.mu.Unlock()
	log.Printf("advisory: %d kayıt yüklendi (%d dosya)", st.records, files)
}

var advDownloadEcos = []string{"npm", "Packagist", "PyPI", "Maven", "Go", "NuGet"}

// downloadAdvisories OSV ekosistem arşivlerini indirir (yalnızca osv-vulnerabilities.storage.googleapis.com).
func (s *Server) downloadAdvisories(ctx context.Context) {
	if s.AdvisoryDir == "" {
		return
	}
	_ = os.MkdirAll(s.AdvisoryDir, 0o755)
	cl := &http.Client{Timeout: 15 * time.Minute}
	for _, eco := range advDownloadEcos {
		dst := filepath.Join(s.AdvisoryDir, eco+".zip")
		if fi, err := os.Stat(dst); err == nil && time.Since(fi.ModTime()) < 20*time.Hour {
			continue
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://osv-vulnerabilities.storage.googleapis.com/"+eco+"/all.zip", nil)
		resp, err := cl.Do(req)
		if err != nil {
			s.adv.mu.Lock()
			s.adv.lastErr = "indirilemedi: " + err.Error()
			s.adv.mu.Unlock()
			log.Printf("advisory: %s indirilemedi: %v", eco, err)
			return
		}
		tmp := dst + ".tmp"
		f, err := os.Create(tmp)
		if err == nil {
			_, err = io.Copy(f, io.LimitReader(resp.Body, 1<<30))
			f.Close()
		}
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			os.Remove(tmp)
			log.Printf("advisory: %s indirme hatası (%d): %v", eco, resp.StatusCode, err)
			continue
		}
		os.Rename(tmp, dst)
	}
	s.adv.mu.Lock()
	s.adv.lastErr = ""
	s.adv.mu.Unlock()
}

// StartAdvisories yükleyiciyi arka planda başlatır: açılışta ve saatte bir dizin değişmişse yeniden yükler.
func (s *Server) StartAdvisories(ctx context.Context) {
	if s.AdvisoryDir == "" {
		return
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if s.AdvisoryAuto {
				s.downloadAdvisories(ctx)
			}
			s.loadAdvisories()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// ---- Sürüm karşılaştırma ve eşleme ----

var verSplitRe = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)(.*)$`)

func cmpVer(a, b string) int {
	ma, mb := verSplitRe.FindStringSubmatch(strings.TrimSpace(a)), verSplitRe.FindStringSubmatch(strings.TrimSpace(b))
	if ma == nil || mb == nil {
		return strings.Compare(a, b)
	}
	pa, pb := strings.Split(ma[1], "."), strings.Split(mb[1], ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	tag := func(s string) string {
		s = strings.ToLower(strings.TrimLeft(s, ".-_+"))
		switch s {
		case "", "release", "final", "ga", "sp1":
			return ""
		}
		return s
	}
	ta, tb := tag(ma[2]), tag(mb[2])
	switch {
	case ta == tb:
		return 0
	case ta == "":
		return 1
	case tb == "":
		return -1
	}
	return strings.Compare(ta, tb)
}

func (e *advEntry) affects(ver string) (bool, string) {
	for _, v := range e.Versions {
		if v == ver {
			return true, ""
		}
	}
	for _, evs := range e.Ranges {
		in, fixed := false, ""
		start := ""
		for _, ev := range evs {
			switch {
			case ev.Introduced != "":
				in, start = true, ev.Introduced
				if start == "0" || cmpVer(ver, start) >= 0 {
					in = true
				} else {
					in = false
				}
			case ev.Fixed != "":
				if in && cmpVer(ver, ev.Fixed) < 0 {
					return true, ev.Fixed
				}
				in = false
			case ev.LastAffected != "":
				if in && cmpVer(ver, ev.LastAffected) <= 0 {
					return true, ""
				}
				in = false
			}
		}
		if in { // açık uçlu aralık (düzeltme yok)
			return true, fixed
		}
	}
	return false, ""
}

type advMatch struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Severity string   `json:"severity"`
	Fixed    string   `json:"fixed,omitempty"`
}

func (s *Server) advMatch(eco, name, ver string) []advMatch {
	s.adv.mu.RLock()
	defer s.adv.mu.RUnlock()
	var out []advMatch
	seen := map[string]bool{}
	for _, e := range s.adv.idx[advKey(eco, name)] {
		if seen[e.ID] {
			continue
		}
		if ok, fixed := e.affects(ver); ok {
			seen[e.ID] = true
			out = append(out, advMatch{ID: e.ID, Aliases: e.Aliases, Summary: e.Summary, Severity: e.Severity, Fixed: fixed})
		}
	}
	return out
}

func (s *Server) advInfo() map[string]any {
	s.adv.mu.RLock()
	defer s.adv.mu.RUnlock()
	m := map[string]any{"dir": s.AdvisoryDir, "enabled": s.AdvisoryDir != "", "auto": s.AdvisoryAuto, "records": s.adv.records, "loading": s.adv.loading}
	if !s.adv.loadedAt.IsZero() {
		m["loaded_at"] = s.adv.loadedAt
	}
	if s.adv.lastErr != "" {
		m["error"] = s.adv.lastErr
	}
	return m
}
