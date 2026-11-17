package crawl

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// Robots bir robots.txt dosyasının bu tarayıcı için geçerli kurallarıdır (RFC 9309).
type Robots struct {
	rules    []rule
	Delay    float64  // Crawl-delay (sn); 0 = belirtilmemiş
	Sitemaps []string // Sitemap: satırları
	DenyAll  bool     // dosya alınamadı (5xx / ağ hatası): kurala göre her şey yasak sayılır
	Found    bool
}

type rule struct {
	allow bool
	pat   string
	re    *regexp.Regexp
	n     int // eşleşme uzunluğu için desen uzunluğu
}

// ParseRobots verilen ürün adı (ör. "appmon") için kurulan grubu seçer; yoksa "*" grubu kullanılır.
func ParseRobots(body string, agent string) *Robots {
	agent = strings.ToLower(agent)
	type group struct {
		agents []string
		rules  []rule
		delay  float64
	}
	var groups []*group
	var cur *group
	lastWasAgent := false
	r := &Robots{Found: true}
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 512*1024)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if !lastWasAgent || cur == nil {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(v))
			lastWasAgent = true
			continue
		case "allow", "disallow":
			if cur != nil && v != "" {
				cur.rules = append(cur.rules, mkRule(k == "allow", v))
			}
		case "crawl-delay":
			if cur != nil {
				if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
					cur.delay = f
				}
			}
		case "sitemap":
			if len(r.Sitemaps) < 5 && v != "" {
				r.Sitemaps = append(r.Sitemaps, v)
			}
		}
		lastWasAgent = false
	}
	var pick *group
	for _, g := range groups {
		for _, a := range g.agents {
			if a == agent || (a != "*" && a != "" && strings.Contains(agent, a)) {
				pick = g
			}
		}
	}
	if pick == nil {
		for _, g := range groups {
			for _, a := range g.agents {
				if a == "*" && pick == nil {
					pick = g
				}
			}
		}
	}
	if pick != nil {
		r.rules, r.Delay = pick.rules, pick.delay
	}
	return r
}

func mkRule(allow bool, pat string) rule {
	re := "^"
	for _, c := range pat {
		switch c {
		case '*':
			re += ".*"
		case '$':
			re += "$"
		default:
			re += regexp.QuoteMeta(string(c))
		}
	}
	x, err := regexp.Compile(re)
	if err != nil {
		x = regexp.MustCompile("^$a")
	}
	return rule{allow: allow, pat: pat, re: x, n: len(pat)}
}

// Allowed yol (sorgu dahil) için tarama iznini döndürür: en uzun eşleşen kural kazanır, eşitlikte Allow.
func (r *Robots) Allowed(pathQuery string) bool {
	if r == nil {
		return true
	}
	if r.DenyAll {
		return false
	}
	if pathQuery == "" {
		pathQuery = "/"
	}
	best, allow := -1, true
	for _, ru := range r.rules {
		if ru.re.MatchString(pathQuery) && (ru.n > best || (ru.n == best && ru.allow)) {
			best, allow = ru.n, ru.allow
		}
	}
	return allow
}
