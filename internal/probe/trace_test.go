package probe

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
)

func TestParseTraceReply(t *testing.T) {
	// TTL doldu: ICMP tür 11 + özgün IP başlığı (20 bayt) + özgün echo (id=0x1234, seq=9)
	orig := make([]byte, 28)
	orig[0], orig[9] = 0x45, 1
	orig[20] = 8
	binary.BigEndian.PutUint16(orig[24:], 0x1234)
	binary.BigEndian.PutUint16(orig[26:], 9)
	msg := append([]byte{11, 0, 0, 0, 0, 0, 0, 0}, orig...)
	orig[1] = 46 << 2
	msg = append([]byte{11, 0, 0, 0, 0, 0, 0, 0}, orig...)
	if k, s, tos := parseTraceReply(msg, 0x1234); k != "exceeded" || s != 9 || tos != 46<<2 {
		t.Fatalf("%s %d %d", k, s, tos)
	}
	if k, _, _ := parseTraceReply(msg, 0x9999); k != "" {
		t.Fatal("başka id kabul edildi")
	}
	rep := []byte{0, 0, 0, 0, 0x12, 0x34, 0, 5}
	if k, s, _ := parseTraceReply(rep, 0x1234); k != "reply" || s != 5 {
		t.Fatalf("%s %d", k, s)
	}
	if k, _, _ := parseTraceReply([]byte{1, 2}, 1); k != "" {
		t.Fatal("kısa paket")
	}
}

func TestRouteChanged(t *testing.T) {
	if routeChanged("t1", "a>b") {
		t.Fatal("ilk ölçüm değişim sayılmamalı")
	}
	if routeChanged("t1", "a>b") {
		t.Fatal("aynı rota")
	}
	if !routeChanged("t1", "a>c") {
		t.Fatal("değişiklik algılanmalı")
	}
}

func TestTraceLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := Trace(ctx, "127.0.0.1", 5, 2, 500*time.Millisecond, false, 0)
	if err != nil {
		t.Skipf("ham ICMP yok: %v", err)
	}
	if !r.Reached || len(r.Hops) != 1 || r.Hops[0].IP != "127.0.0.1" {
		t.Fatalf("%+v", r)
	}
}

func TestMarkChange(t *testing.T) {
	d := func(v int) *int { return &v }
	hops := []Hop{{TTL: 1, DSCPIn: d(46)}, {TTL: 2}, {TTL: 3, DSCPIn: d(46)}, {TTL: 4, DSCPIn: d(0)}, {TTL: 5, DSCPIn: d(0)}}
	if ttl, seen := markChange(hops, 46); ttl != 4 || seen != 4 {
		t.Fatalf("ttl=%d seen=%d", ttl, seen)
	}
	if ttl, seen := markChange(hops[:3], 46); ttl != 0 || seen != 2 {
		t.Fatalf("ttl=%d seen=%d", ttl, seen)
	}
	if ttl, seen := markChange([]Hop{{TTL: 1}}, 46); ttl != 0 || seen != 0 {
		t.Fatalf("ttl=%d seen=%d", ttl, seen)
	}
}
