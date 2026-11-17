package main

import "testing"

func TestParseProcNet(t *testing.T) {
	data := []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 11111 1 0 100 0 0 10 0
   1: 0400000A:C351 0500000A:1538 01 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0 20 4 28 10 -1
   2: 0100007F:C352 0100007F:1F40 06 00000000:00000000 00:00000000 00000000  1000        0 33333 1 0 20 4 28 10 -1
`)
	m := parseProcNet(data)
	if len(m) != 1 {
		t.Fatalf("yalnızca ESTABLISHED: %+v", m)
	}
	s := m["22222"]
	if s.lip != "10.0.0.4" || s.lport != 0xC351 || s.rip != "10.0.0.5" || s.rport != 5432 {
		t.Fatalf("yuva: %+v", s)
	}
	if ip := hexIP("00000000000000000000000001000000"); ip != "::1" {
		t.Fatalf("IPv6 loopback: %q", ip)
	}
	if ip := hexIP("0000000000000000FFFF00000100007F"); ip != "127.0.0.1" {
		t.Fatalf("IPv4-eşlemeli IPv6: %q", ip)
	}
}
