package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Çalışma zamanı / servis destek sonu (EOL) tablosu. Yerleşik değerler 2026-11 itibarıyla topluluk (upstream) takvimidir;
// dağıtıcı (RHEL, Ubuntu LTS) veya Oracle/Red Hat uzatılmış desteği farklı olabilir. Veri dizinine eol.json
// ({"node":{"20":"2026-04-30"}}) konarak değerler ezilebilir/eklenebilir.
var eolTable = map[string]map[string]string{
	"node": {"12": "2022-04-30", "14": "2023-04-30", "16": "2023-09-11", "17": "2022-06-01", "18": "2025-04-30", "19": "2023-06-01", "20": "2026-04-30",
		"21": "2024-06-01", "22": "2027-04-30", "23": "2025-06-01", "24": "2028-04-30", "25": "2026-06-01", "26": "2029-04-30"},
	"java": {"6": "2018-12-31", "7": "2022-07-31", "8": "2026-12-31", "9": "2018-03-31", "10": "2018-09-30", "11": "2027-10-31", "12": "2019-09-30", "13": "2020-03-31",
		"14": "2020-09-30", "15": "2021-03-31", "16": "2021-09-30", "17": "2027-10-31", "18": "2022-09-30", "19": "2023-03-31", "20": "2023-09-30", "21": "2029-12-31",
		"22": "2024-09-30", "23": "2025-03-31", "24": "2025-09-30", "25": "2031-09-30", "26": "2026-09-30"},
	"php": {"5.6": "2018-12-31", "7.0": "2018-12-03", "7.1": "2019-12-01", "7.2": "2020-11-30", "7.3": "2021-12-06", "7.4": "2022-11-28", "8.0": "2023-11-26",
		"8.1": "2025-12-31", "8.2": "2026-12-31", "8.3": "2027-12-31", "8.4": "2028-12-31", "8.5": "2029-12-31"},
	"python": {"2.7": "2020-01-01", "3.5": "2020-09-30", "3.6": "2021-12-23", "3.7": "2023-06-27", "3.8": "2024-10-07", "3.9": "2025-10-31", "3.10": "2026-10-31",
		"3.11": "2027-10-31", "3.12": "2028-10-31", "3.13": "2029-10-31", "3.14": "2030-10-31"},
	"ruby":       {"2.5": "2021-03-31", "2.6": "2022-03-31", "2.7": "2023-03-31", "3.0": "2024-03-31", "3.1": "2025-03-31", "3.2": "2026-03-31", "3.3": "2027-03-31", "3.4": "2028-03-31"},
	"dotnet":     {"3": "2022-12-13", "5": "2022-05-10", "6": "2024-11-12", "7": "2024-05-14", "8": "2026-11-10", "9": "2026-11-10", "10": "2028-11-14"},
	"httpd":      {"2.0": "2013-07-10", "2.2": "2017-12-31"},
	"postgresql": {"9": "2021-11-11", "10": "2022-11-10", "11": "2023-11-09", "12": "2024-11-14", "13": "2025-11-13", "14": "2026-11-12", "15": "2027-11-11", "16": "2028-11-09", "17": "2029-11-08", "18": "2030-11-14"},
	"mysql":      {"5.5": "2018-12-31", "5.6": "2021-02-28", "5.7": "2023-10-31", "8.0": "2026-04-30", "8.4": "2032-04-30"},
	"mariadb":    {"10.1": "2020-10-17", "10.2": "2022-05-23", "10.3": "2023-05-25", "10.4": "2024-06-18", "10.5": "2025-06-24", "10.6": "2026-07-06", "10.11": "2028-02-10", "11.4": "2029-05-19"},
	"mongodb":    {"3.6": "2021-04-30", "4.0": "2022-04-30", "4.2": "2023-04-30", "4.4": "2024-02-29", "5.0": "2024-10-31", "6.0": "2025-07-31", "7.0": "2027-08-31", "8.0": "2029-10-31"},
	"go": {"1.18": "2023-02-01", "1.19": "2023-09-01", "1.20": "2024-02-01", "1.21": "2024-08-01", "1.22": "2025-02-01", "1.23": "2025-08-01", "1.24": "2026-02-01",
		"1.25": "2026-08-01", "1.26": "2027-02-01", "1.27": "2027-08-01"},
}

// eolCycle sürüm dizesinden, tablodaki anahtar biçimine uygun sürüm hattını çıkarır.
func eolCycle(rt, ver string) string {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	parts := strings.FieldsFunc(ver, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == 'p' })
	if len(parts) == 0 {
		return ""
	}
	major, _ := strconv.Atoi(parts[0])
	minor := ""
	if len(parts) > 1 {
		minor = parts[1]
	}
	switch rt {
	case "node", "dotnet":
		return parts[0]
	case "java":
		if major == 1 && minor != "" { // 1.8.0_392 -> 8
			return minor
		}
		return parts[0]
	case "postgresql":
		if major >= 10 {
			return parts[0]
		}
		return parts[0]
	case "php", "python", "ruby", "httpd", "mysql", "mariadb", "mongodb", "go":
		if minor == "" {
			return ""
		}
		return parts[0] + "." + minor
	}
	return ""
}

type eolInfo struct {
	Status string `json:"status"` // eol | soon | ok | unknown
	Date   string `json:"date,omitempty"`
	Cycle  string `json:"cycle,omitempty"`
}

func (s *Server) eolLookup(rt, ver string, now time.Time) eolInfo {
	cycle := eolCycle(rt, ver)
	if cycle == "" {
		return eolInfo{Status: "unknown"}
	}
	tbl := s.eolTable()
	d, ok := tbl[rt][cycle]
	if !ok {
		// Tablonun en eski kaydından da eski bir hat için (ör. python 2.6) destek sonu varsayılır.
		if old, ok2 := tbl[rt]; ok2 && olderThanTable(old, cycle, now) {
			return eolInfo{Status: "eol", Cycle: cycle}
		}
		return eolInfo{Status: "unknown", Cycle: cycle}
	}
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return eolInfo{Status: "unknown", Cycle: cycle}
	}
	switch {
	case !now.Before(t):
		return eolInfo{Status: "eol", Date: d, Cycle: cycle}
	case t.Sub(now) < 180*24*time.Hour:
		return eolInfo{Status: "soon", Date: d, Cycle: cycle}
	}
	return eolInfo{Status: "ok", Date: d, Cycle: cycle}
}

// olderThanTable tabloda olmayan bir hat, kendisinden yeni ilk kaydın destek sonu geçmişse (ör. python 3.2 < 3.5) EOL sayılır.
func olderThanTable(tbl map[string]string, cycle string, now time.Time) bool {
	next, nextDate := "", ""
	for k, d := range tbl {
		if cmpVer(k, cycle) > 0 && (next == "" || cmpVer(k, next) < 0) {
			next, nextDate = k, d
		}
	}
	if next == "" {
		return false
	}
	t, err := time.Parse("2006-01-02", nextDate)
	return err == nil && !now.Before(t)
}

// eolTable yerleşik tabloyu veri dizinindeki eol.json ile birleştirir.
func (s *Server) eolTable() map[string]map[string]string {
	if s.AdvisoryDir == "" {
		return eolTable
	}
	b, err := os.ReadFile(filepath.Join(s.AdvisoryDir, "eol.json"))
	if err != nil {
		return eolTable
	}
	var over map[string]map[string]string
	if json.Unmarshal(b, &over) != nil {
		return eolTable
	}
	out := map[string]map[string]string{}
	for k, v := range eolTable {
		m := map[string]string{}
		for c, d := range v {
			m[c] = d
		}
		out[k] = m
	}
	for k, v := range over {
		if out[k] == nil {
			out[k] = map[string]string{}
		}
		for c, d := range v {
			out[k][c] = d
		}
	}
	return out
}
