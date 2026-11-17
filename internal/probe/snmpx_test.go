package probe

import (
	"bytes"
	"context"
	"net"
	"sort"
	"sync"
	"testing"
	"time"
)

// dinamik MIB: sayaçlar testte değiştirilebilir.
type dynMIB struct {
	sync.Mutex
	m []mibEntry
}

func (d *dynMIB) set(oid string, t byte, v []byte) {
	d.Lock()
	defer d.Unlock()
	for i := range d.m {
		if d.m[i].oid == oid {
			d.m[i].t, d.m[i].v = t, v
			return
		}
	}
	d.m = append(d.m, mibEntry{oid, t, v})
	sort.Slice(d.m, func(i, j int) bool { return oidLess(d.m[i].oid, d.m[j].oid) })
}

func (d *dynMIB) agent(pc net.PacketConn, community string) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		_, body, _, _ := readTLV(buf[:n])
		_, ver, body, _ := readTLV(body)
		_, c, body, _ := readTLV(body)
		if string(c) != community {
			continue
		}
		tag, pdu, _, _ := readTLV(body)
		_, content, _, _ := readTLV(append([]byte{tag}, append(berLen(len(pdu)), pdu...)...))
		d.Lock()
		resp := respPDU(append([]mibEntry(nil), d.m...), tag, content)
		d.Unlock()
		pc.WriteTo(tlv(0x30, bytes.Join([][]byte{tlv(0x02, ver), tlv(0x04, c), resp}, nil)), addr)
	}
}

func u32(n uint64) []byte {
	b := []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	return b
}

func TestSNMPXInterfacesAndCustomRate(t *testing.T) {
	d := &dynMIB{}
	d.set("1.3.6.1.2.1.1.3.0", 0x43, intV(5000))
	d.set(oidIfDescr+".1", 0x04, []byte("eth0"))
	d.set(oidIfDescr+".2", 0x04, []byte("lo"))
	d.set(oidIfOper+".1", 0x02, intV(1))
	d.set(oidIfOper+".2", 0x02, intV(1))
	d.set(oidIfHighSpd+".1", 0x42, intV(100)) // 100 Mbps
	d.set(oidIfInOct+".1", 0x41, u32(1000))
	d.set(oidIfOutOct+".1", 0x41, u32(4294967000)) // sarmaya yakın
	d.set("1.3.6.1.4.1.9999.1.0", 0x02, intV(250))
	addr := startAgent(t, func(pc net.PacketConn) { d.agent(pc, "gizli") })
	o := Options{Community: "gizli", Template: "interfaces", IfFilter: "^eth", OIDs: []CustomOID{{Name: "sensor", OID: "1.3.6.1.4.1.9999.1.0", Scale: 0.1}}}
	if err := ValidateSNMPX(o); err != nil {
		t.Fatal(err)
	}
	run := func() map[string]float64 {
		r := RunX(context.Background(), Spec{Type: "snmpx", Target: addr, Timeout: 5 * time.Second, Opts: o}, false)
		if r.Err != nil {
			t.Fatalf("hata: %v", r.Err)
		}
		return r.Extra
	}
	x1 := run()
	if x1["net.eth0.up"] != 1 || x1["snmp.sensor"] != 25 {
		t.Fatalf("ilk çalıştırma: %v", x1)
	}
	if _, ok := x1["net.lo.up"]; ok {
		t.Fatal("filtre lo'yu elemeli")
	}
	if _, ok := x1["net.eth0.in_bps"]; ok {
		t.Fatal("ilk örnekte hız olmamalı")
	}
	time.Sleep(1100 * time.Millisecond)
	d.set(oidIfInOct+".1", 0x41, u32(1000+125000)) // ~1.1 sn'de 125000 bayt
	d.set(oidIfOutOct+".1", 0x41, u32(500))        // 32 bit sarma: (2^32-4294967000)+500 = 796 bayt
	x2 := run()
	in := x2["net.eth0.in_bps"]
	if in < 700000 || in > 1100000 {
		t.Fatalf("in_bps = %v, ~909000 bekleniyordu; %v", in, x2)
	}
	if out := x2["net.eth0.out_bps"]; out <= 0 || out > 20000 {
		t.Fatalf("sarma sonrası out_bps = %v", out)
	}
	if u := x2["net.eth0.util_pct"]; u < 0.5 || u > 1.5 {
		t.Fatalf("util_pct = %v", u)
	}
}

func TestSNMPXTemplatesAndValidation(t *testing.T) {
	d := &dynMIB{}
	d.set("1.3.6.1.2.1.1.3.0", 0x43, intV(1))
	d.set("1.3.6.1.4.1.2021.11.11.0", 0x02, intV(70)) // idle %70 -> cpu %30
	d.set("1.3.6.1.4.1.2021.4.5.0", 0x02, intV(1000))
	d.set("1.3.6.1.4.1.2021.4.6.0", 0x02, intV(400))
	d.set("1.3.6.1.4.1.2021.4.14.0", 0x02, intV(100))
	d.set("1.3.6.1.4.1.2021.4.15.0", 0x02, intV(100))
	d.set("1.3.6.1.4.1.2021.10.1.5.1", 0x02, intV(150))
	addr := startAgent(t, func(pc net.PacketConn) { d.agent(pc, "public") })
	r := RunX(context.Background(), Spec{Type: "snmpx", Target: addr, Timeout: 5 * time.Second, Opts: Options{Template: "linux"}}, false)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if v := r.Extra["host.cpu_pct"]; v < 29.9 || v > 30.1 {
		t.Fatalf("cpu = %v", v)
	}
	if v := r.Extra["host.mem_pct"]; v < 39.9 || v > 40.1 { // (1000-400-100-100)/1000
		t.Fatalf("mem = %v", v)
	}
	if v := r.Extra["host.load1"]; v != 1.5 {
		t.Fatalf("load1 = %v", v)
	}
	for _, bad := range []Options{{}, {Template: "yok"}, {OIDs: []CustomOID{{Name: "a b", OID: "1.3.6.1"}}}, {OIDs: []CustomOID{{Name: "x", OID: "abc"}}}, {Template: "interfaces", IfFilter: "("}} {
		if ValidateSNMPX(bad) == nil {
			t.Errorf("geçersiz seçenek kabul edildi: %+v", bad)
		}
	}
}
