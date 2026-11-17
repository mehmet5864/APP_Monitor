package main

import "testing"

func TestBuildConns(t *testing.T) {
	pidName := map[int]string{10: "nginx", 20: "gunicorn", 30: "postgres", 40: "curl"}
	listen := map[int][]int{10: {80}, 20: {8000}, 30: {5432}}
	local := map[string]bool{"10.0.0.4": true}
	socks := []sock{
		// nginx -> gunicorn (loopback, istemci tarafı) ve sunucu tarafı (atlanmalı)
		{lip: "127.0.0.1", rip: "127.0.0.1", lport: 50001, rport: 8000, pid: 10},
		{lip: "127.0.0.1", rip: "127.0.0.1", lport: 8000, rport: 50001, pid: 20},
		{lip: "127.0.0.1", rip: "127.0.0.1", lport: 50002, rport: 8000, pid: 10},
		// gunicorn -> uzak postgres, gunicorn -> dış https
		{lip: "10.0.0.4", rip: "10.0.0.5", lport: 50003, rport: 5432, pid: 20},
		{lip: "10.0.0.4", rip: "10.0.0.5", lport: 50004, rport: 5432, pid: 20},
		{lip: "10.0.0.4", rip: "93.184.216.34", lport: 50005, rport: 443, pid: 20},
		// kendi IP'sinden yerel postgres'e (loopback değil ama bu makine)
		{lip: "10.0.0.4", rip: "10.0.0.4", lport: 50006, rport: 5432, pid: 20},
		// gelen bağlantı (nginx 80 dinliyor) atlanır
		{lip: "10.0.0.4", rip: "203.0.113.9", lport: 80, rport: 40000, pid: 10},
		// sahibi bilinmeyen yuva (pid 99) ve bilinmeyen yerel hedef atlanır
		{lip: "10.0.0.4", rip: "10.0.0.6", lport: 50007, rport: 22, pid: 99},
		{lip: "127.0.0.1", rip: "127.0.0.1", lport: 50008, rport: 9999, pid: 40},
	}
	got := buildConns(socks, pidName, listen, local)
	idx := map[string]connRec{}
	for _, e := range got {
		idx[e.Src+">"+e.Dst+"|"+e.Addr] = e
	}
	if len(got) != 4 {
		t.Fatalf("4 grup beklenir: %+v", got)
	}
	if e := idx["nginx>gunicorn|127.0.0.1"]; e.N != 2 || e.Port != 8000 {
		t.Fatalf("nginx->gunicorn: %+v", got)
	}
	if e := idx["gunicorn>|10.0.0.5"]; e.N != 2 || e.Port != 5432 {
		t.Fatalf("gunicorn->uzak postgres: %+v", got)
	}
	if e := idx["gunicorn>|93.184.216.34"]; e.N != 1 || e.Port != 443 {
		t.Fatalf("dış: %+v", got)
	}
	if e := idx["gunicorn>postgres|10.0.0.4"]; e.N != 1 {
		t.Fatalf("kendi IP'sinden yerel postgres: %+v", got)
	}
	if got[0].N < got[len(got)-1].N {
		t.Fatalf("sıra azalan olmalı: %+v", got)
	}
}

func TestParseNetstat(t *testing.T) {
	out := `Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:80             0.0.0.0:0              LISTENING       4
  TCP    10.0.0.4:50000         10.0.0.5:5432          ESTABLISHED     1234
  TCP    [::1]:50001            [::1]:8000             ESTABLISHED     55
  TCP    10.0.0.4:50002         93.184.216.34:443      TIME_WAIT       0
`
	s := parseNetstat(out)
	if len(s) != 2 || s[0].rip != "10.0.0.5" || s[0].rport != 5432 || s[0].pid != 1234 || s[1].lip != "::1" || s[1].rport != 8000 {
		t.Fatalf("netstat çözümü: %+v", s)
	}
}
