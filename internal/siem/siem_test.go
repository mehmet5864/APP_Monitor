package siem

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var ev = Event{Time: time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC), Type: "audit", Severity: "info", Actor: "bootstrap-admin", Action: "probe.create", Detail: `tcp a]b "x"`}

func TestFormats(t *testing.T) {
	c := Config{Protocol: "udp", Host: "h", Format: "rfc5424", Audit: true}
	Validate(&c)
	s := string(c.Render(ev))
	if !strings.HasPrefix(s, "<134>1 2026-11-06T01:02:03Z ") || !strings.Contains(s, `[appmon@32473 type="audit"`) || !strings.Contains(s, `detail="tcp a\]b \"x\""`) {
		t.Fatal(s)
	}
	c.Format = "cef"
	s = string(c.Render(ev))
	if !strings.Contains(s, "CEF:0|APP_MON|APP_MON|1.0|audit.probe.create|") || !strings.Contains(s, "suser=bootstrap-admin") {
		t.Fatal(s)
	}
	c.Format = "json"
	s = string(c.Render(ev))
	if !strings.Contains(s, `"actor":"bootstrap-admin"`) || !strings.Contains(s, "<134>1 ") {
		t.Fatal(s)
	}
	al := Event{Time: ev.Time, Type: "alert", Severity: "critical", Action: "open", Asset: "web", Rule: "Disk"}
	if p := string(c.Render(al)); !strings.HasPrefix(p, "<130>") {
		t.Fatal(p)
	}
}

func TestWanted(t *testing.T) {
	c := Config{Audit: false, Alerts: true, MinSev: "warning"}
	if c.Wanted(ev) || c.Wanted(Event{Type: "alert", Severity: "info"}) || !c.Wanted(Event{Type: "alert", Severity: "critical"}) || !c.Wanted(Event{Type: "test"}) {
		t.Fatal("Wanted")
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []Config{{Protocol: "x"}, {Protocol: "udp", Audit: true}, {Protocol: "http", Audit: true, URL: "ftp://x"}, {Protocol: "http", Audit: true, URL: "http://x", Auth: "splunk"}, {Protocol: "udp", Host: "h"}} {
		if Validate(&c) == nil {
			t.Fatalf("%+v kabul edildi", c)
		}
	}
	c := Config{Protocol: "tls", Host: "h", Audit: true}
	if err := Validate(&c); err != nil || c.Port != 6514 || c.Facility != 16 {
		t.Fatal(err, c)
	}
}

func TestTransports(t *testing.T) {
	ctx := context.Background()
	// UDP
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	_, port, _ := net.SplitHostPort(pc.LocalAddr().String())
	p := atoi(port)
	if err := Send(ctx, Config{Protocol: "udp", Host: "127.0.0.1", Port: p, Audit: true}, ev, Env{}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "probe.create") {
		t.Fatal(err, string(buf[:n]))
	}
	// TCP
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	got := make(chan string, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { l, _ := bufio.NewReader(c).ReadString('\n'); got <- l; c.Close() }()
		}
	}()
	_, tp, _ := net.SplitHostPort(ln.Addr().String())
	if err := Send(ctx, Config{Protocol: "tcp", Host: "127.0.0.1", Port: atoi(tp), Format: "cef", Audit: true}, ev, Env{}); err != nil {
		t.Fatal(err)
	}
	if l := <-got; !strings.Contains(l, "CEF:0") || !strings.HasSuffix(l, "\n") {
		t.Fatal(l)
	}
	// TLS
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	defer cert.Close()
	tl, _ := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: cert.TLS.Certificates})
	defer tl.Close()
	go func() {
		c, err := tl.Accept()
		if err == nil {
			bufio.NewReader(c).ReadString('\n')
			c.Close()
		}
	}()
	_, lp, _ := net.SplitHostPort(tl.Addr().String())
	cfg := Config{Protocol: "tls", Host: "127.0.0.1", Port: atoi(lp), Audit: true}
	if err := Send(ctx, cfg, ev, Env{}); err == nil {
		t.Fatal("sertifika doğrulanmadan kabul edildi")
	}
	go func() {
		c, err := tl.Accept()
		if err == nil {
			l, _ := bufio.NewReader(c).ReadString('\n')
			got <- l
			c.Close()
		}
	}()
	cfg.SkipTLS = true
	if err := Send(ctx, cfg, ev, Env{}); err != nil {
		t.Fatal(err)
	}
	if l := <-got; !strings.Contains(l, "appmon@32473") {
		t.Fatal(l)
	}
	// HTTP (Splunk HEC)
	var auth, body string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer hs.Close()
	if err := Send(ctx, Config{Protocol: "http", URL: hs.URL, Auth: "splunk", Token: "tok", Audit: true}, ev, Env{}); err != nil {
		t.Fatal(err)
	}
	if auth != "Splunk tok" || !strings.Contains(body, `"event":{`) || !strings.Contains(body, "probe.create") {
		t.Fatal(auth, body)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 403) }))
	defer bad.Close()
	if err := Send(ctx, Config{Protocol: "http", URL: bad.URL, Audit: true}, ev, Env{}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatal(err)
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
