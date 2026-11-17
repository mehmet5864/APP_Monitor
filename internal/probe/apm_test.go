package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleSS = `Recv-Q Send-Q Local Address:Port Peer Address:Port Process
0      0      10.0.0.5:52344  10.0.0.9:5432
	 cubic wscale:7,7 rto:204 rtt:0.52/0.25 ato:40 mss:1448
0      0      10.0.0.5:52346  10.0.0.9:5432
	 cubic wscale:7,7 rto:204 rtt:1.52/0.25 ato:40 mss:1448
0      0      10.0.0.5:443    10.0.0.77:61000
	 cubic rtt:20/3
0      0      10.0.0.5:22     10.0.0.200:40000
0      0      127.0.0.1:5432  127.0.0.1:50000
0      0      [::ffff:10.0.0.5]:443 [::ffff:10.0.0.78]:61001
0      0      10.0.0.5:40000  10.0.0.5:8080
`

func TestParseConns(t *testing.T) {
	split := func(s string) []string {
		var out []string
		cur := ""
		for _, c := range s {
			if c == '\n' {
				out = append(out, cur)
				cur = ""
			} else {
				cur += string(c)
			}
		}
		return append(out, cur)
	}
	listen := []string{"State Recv-Q Send-Q Local Address:Port Peer Address:Port", "LISTEN 0 128 0.0.0.0:443 0.0.0.0:*", "LISTEN 0 128 0.0.0.0:22 0.0.0.0:*", "LISTEN 0 128 [::]:8080 [::]:*"}
	snap := ParseConns(split(sampleSS), listen, []string{"10.0.0.5 fe80::1 172.17.0.1"})
	if len(snap.Addrs) != 3 {
		t.Fatalf("adresler: %v", snap.Addrs)
	}
	find := func(dir, peer string, port int) *Conn {
		for i := range snap.Conns {
			c := &snap.Conns[i]
			if c.Dir == dir && c.Peer == peer && c.Port == port {
				return c
			}
		}
		return nil
	}
	db := find("out", "10.0.0.9", 5432)
	if db == nil || db.N != 2 || db.RTT < 1.0 || db.RTT > 1.03 {
		t.Fatalf("db bağlantısı yanlış: %+v", db)
	}
	if c := find("in", "10.0.0.77", 443); c == nil || c.N != 1 || c.RTT != 20 {
		t.Fatalf("gelen 443: %+v", c)
	}
	if c := find("in", "10.0.0.78", 443); c == nil {
		t.Fatalf("IPv4-eşlenmiş adres çözülmedi")
	}
	if c := find("in", "10.0.0.200", 22); c == nil || c.RTT != -1 {
		t.Fatalf("rtt'siz bağlantı: %+v", c)
	}
	for _, c := range snap.Conns {
		if c.Peer == "127.0.0.1" || c.Peer == "10.0.0.5" {
			t.Fatalf("loopback/kendi adresi atlanmalı: %+v", c)
		}
	}
}

func TestHTTPTiming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.Write([]byte("tamam"))
	}))
	defer srv.Close()
	res := RunX(context.Background(), Spec{Type: "http", Target: srv.URL, Timeout: 5 * time.Second}, false)
	if !res.Up || res.Timing == nil {
		t.Fatalf("up=%v timing=%v err=%v", res.Up, res.Timing, res.Err)
	}
	if res.Timing["ttfb"] < 50 || res.Timing["connect"] <= 0 && res.Timing["total"] <= 0 {
		t.Fatalf("kırılım beklenenden farklı: %v", res.Timing)
	}
	if _, ok := res.Extra["http_ttfb_ms"]; !ok {
		t.Fatalf("http_ttfb_ms extra yok: %v", res.Extra)
	}
	// Bağlanamayan hedefte kırılım yok, probe başarısız.
	res = RunX(context.Background(), Spec{Type: "http", Target: "http://127.0.0.1:1/", Timeout: time.Second}, false)
	if res.Up || res.Timing != nil {
		t.Fatalf("başarısız probe'ta kırılım olmamalı")
	}
}
