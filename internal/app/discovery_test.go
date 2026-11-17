package app

import (
	"context"
	"net"
	"testing"
)

func TestExpandCIDR(t *testing.T) {
	ips, err := expandCIDR("192.168.1.0/24")
	if err != nil || len(ips) != 254 || ips[0].String() != "192.168.1.1" || ips[253].String() != "192.168.1.254" {
		t.Fatalf("%d %v", len(ips), err)
	}
	if ips, _ := expandCIDR("10.0.0.5/32"); len(ips) != 1 {
		t.Fatal("/32 tek adres olmalı")
	}
	for _, bad := range []string{"10.0.0.0/21", "::1/128", "abc", "10.0.0.0/8"} {
		if _, err := expandCIDR(bad); err == nil {
			t.Errorf("geçersiz kabul edildi: %s", bad)
		}
	}
}

func TestScanHostFindsOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	h, ok := scanHost(context.Background(), net.ParseIP("127.0.0.1"), []int{port, 1})
	if !ok || len(h.Ports) != 1 || h.Ports[0] != port {
		t.Fatalf("%+v %v", h, ok)
	}
}
