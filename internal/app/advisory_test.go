package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCmpVer(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"1.2.3", "1.2.4", -1}, {"1.10.0", "1.9.9", 1}, {"2.0", "2.0.0", 0}, {"1.0.0-beta", "1.0.0", -1}, {"v1.2.3", "1.2.3", 0}, {"4.17.20", "4.17.21", -1}, {"5.3.1.RELEASE", "5.3.1", 0}}
	for _, c := range cases {
		if got := cmpVer(c.a, c.b); got != c.want {
			t.Errorf("cmpVer(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAdvisoryMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"id":"GHSA-xxxx","aliases":["CVE-2021-23337"],"summary":"lodash command injection","database_specific":{"severity":"HIGH"},
	  "affected":[{"package":{"ecosystem":"npm","name":"lodash"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"4.17.21"}]}]}]}`), 0o644)
	os.WriteFile(filepath.Join(dir, "b.json"), []byte(`{"id":"GHSA-yyyy","severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}],
	  "affected":[{"package":{"ecosystem":"Maven","name":"org.springframework:spring-core"},"ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"5.3.0"},{"fixed":"5.3.18"},{"introduced":"6.0.0"},{"fixed":"6.0.2"}]}]}]}`), 0o644)
	os.WriteFile(filepath.Join(dir, "w.json"), []byte(`{"id":"GHSA-wd","withdrawn":"2021-01-01T00:00:00Z","affected":[{"package":{"ecosystem":"npm","name":"x"},"versions":["1.0.0"]}]}`), 0o644)
	s := &Server{AdvisoryDir: dir}
	s.loadAdvisories()
	if s.adv.records != 2 {
		t.Fatalf("records=%d", s.adv.records)
	}
	m := s.advMatch("npm", "Lodash", "4.17.20")
	if len(m) != 1 || m[0].Severity != "high" || m[0].Fixed != "4.17.21" || m[0].Aliases[0] != "CVE-2021-23337" {
		t.Fatalf("lodash: %+v", m)
	}
	if len(s.advMatch("npm", "lodash", "4.17.21")) != 0 {
		t.Fatal("fixed version must not match")
	}
	if len(s.advMatch("npm", "x", "1.0.0")) != 0 {
		t.Fatal("withdrawn advisory must be ignored")
	}
	// Maven: yalnızca artifactId ile (jar adından) eşleşir; ikinci aralık da çalışmalı.
	if m := s.advMatch("Maven", "spring-core", "5.3.10"); len(m) != 1 || m[0].Severity != "critical" {
		t.Fatalf("spring 5.3.10: %+v", m)
	}
	if len(s.advMatch("Maven", "spring-core", "5.3.18")) != 0 || len(s.advMatch("Maven", "spring-core", "5.9.0")) != 0 {
		t.Fatal("between ranges must not match")
	}
	if len(s.advMatch("Maven", "spring-core", "6.0.1")) != 1 {
		t.Fatal("second range must match")
	}
}

func TestCVSS3(t *testing.T) {
	if v := cvss3("CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"); v != 9.8 {
		t.Fatalf("9.8 beklenir, %v", v)
	}
	if v := cvss3("CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:L/I:N/A:N"); v != 3.3 {
		t.Fatalf("3.3 beklenir, %v", v)
	}
}

func TestEOL(t *testing.T) {
	s := &Server{}
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	check := func(rt, ver, want string) {
		t.Helper()
		if got := s.eolLookup(rt, ver, now).Status; got != want {
			t.Errorf("%s %s: %s, want %s", rt, ver, got, want)
		}
	}
	check("node", "18.19.0", "eol")
	check("node", "20.11.0", "eol")
	check("node", "22.1.0", "soon") // 2027-04-30
	check("node", "24.1.0", "ok")
	check("java", "1.8.0_392", "soon") // 2026-12-31
	check("java", "17.0.9", "ok")
	check("php", "8.2.7", "soon")
	check("php", "7.4.33", "eol")
	check("python", "3.6.9", "eol")
	check("python", "3.2.0", "eol") // tablonun en eskisinden de eski
	check("postgresql", "14.5", "eol")
	check("go", "1.21.5", "eol")
	check("nginx", "1.24.0", "unknown")
	check("node", "99.0.0", "unknown")
}

func TestInventorySanitize(t *testing.T) {
	inv := &AgentInventory{Listeners: []InvListener{{Port: 0}, {Port: 22, Scope: "x", Proc: "sshd\x00"}, {Port: 70000}}, Deps: []InvDep{{Eco: "evil", Name: "a", Version: "1"}, {Eco: "npm", Name: "lodash", Version: "4.17.20", Src: "zzz"}, {Eco: "npm", Name: "", Version: "1"}}}
	inv.sanitize()
	if len(inv.Listeners) != 1 || inv.Listeners[0].Scope != "net" || inv.Listeners[0].Proc != "sshd" {
		t.Fatalf("listeners: %+v", inv.Listeners)
	}
	if len(inv.Deps) != 1 || inv.Deps[0].Src != "" {
		t.Fatalf("deps: %+v", inv.Deps)
	}
}
