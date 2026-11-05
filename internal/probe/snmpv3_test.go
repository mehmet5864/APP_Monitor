package probe

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// RFC 3414 Ek A.3 test vektörleri.
func TestKeyLocalizationRFC3414(t *testing.T) {
	eng, _ := hex.DecodeString("000000000000000000000002")
	k := localizeKey(md5.New, passwordToKey(md5.New, "maplesyrup"), eng)
	if hex.EncodeToString(k) != "526f5eed9fcce26f8964c2930787d82b" {
		t.Fatalf("MD5 anahtar: %x", k)
	}
	k = localizeKey(sha1.New, passwordToKey(sha1.New, "maplesyrup"), eng)
	if hex.EncodeToString(k) != "6695febc9288e36282235fc7151f128497b38f3f" {
		t.Fatalf("SHA anahtar: %x", k)
	}
}

type mibEntry struct {
	oid string
	t   byte
	v   []byte
}

func oidLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func intV(n int64) []byte { return berInt(n)[2:] }

func fakeMIB() []mibEntry {
	oidB := func(o string) []byte { b, _ := berOID(o); return b[2:] }
	m := []mibEntry{
		{"1.3.6.1.2.1.1.3.0", 0x43, intV(123456)},
		{"1.3.6.1.2.1.25.3.3.1.2.196608", 0x02, intV(40)},
		{"1.3.6.1.2.1.25.3.3.1.2.196609", 0x02, intV(20)},
	}
	rows := []struct {
		i    string
		typ  string
		desc string
		size int64
		used int64
	}{
		{"1", hrStorageRam, "Physical memory", 1000, 800},
		{"2", hrStorageOther, "Cached memory", 300, 300},
		{"3", hrStorageOther, "Memory buffers", 100, 100},
		{"31", hrStorageFixedDisk, "/", 2000, 1000},
		{"32", hrStorageFixedDisk, "/var", 1000, 930},
		{"40", "1.3.6.1.2.1.25.2.1.3", "Swap space", 500, 10},
	}
	for _, r := range rows {
		m = append(m,
			mibEntry{oidHrSType + "." + r.i, 0x06, oidB(r.typ)},
			mibEntry{oidHrSDescr + "." + r.i, 0x04, []byte(r.desc)},
			mibEntry{oidHrSSize + "." + r.i, 0x02, intV(r.size)},
			mibEntry{oidHrSUsed + "." + r.i, 0x02, intV(r.used)})
	}
	sort.Slice(m, func(i, j int) bool { return oidLess(m[i].oid, m[j].oid) })
	return m
}

func lookup(mib []mibEntry, tag byte, oid string) (mibEntry, bool) {
	for _, e := range mib {
		if tag == 0xA0 && e.oid == oid {
			return e, true
		}
		if tag == 0xA1 && oidLess(oid, e.oid) {
			return e, true
		}
	}
	return mibEntry{}, false
}

func respPDU(mib []mibEntry, tag byte, content []byte) []byte {
	_, id, body, _ := readTLV(content)
	_, _, body, _ = readTLV(body)
	_, _, body, _ = readTLV(body)
	_, vbl, _, _ := readTLV(body)
	_, vb, _, _ := readTLV(vbl)
	_, ob, _, _ := readTLV(vb)
	e, ok := lookup(mib, tag, oidString(ob))
	var vbs []byte
	if ok {
		o, _ := berOID(e.oid)
		vbs = tlv(0x30, tlv(0x30, append(o, tlv(e.t, e.v)...)))
	} else {
		o, _ := berOID(oidString(ob))
		vbs = tlv(0x30, tlv(0x30, append(o, 0x82, 0x00)))
	}
	return tlv(0xA2, append(append(append(tlv(0x02, id), berInt(0)...), berInt(0)...), vbs...))
}

func mustOID(o string) []byte { b, _ := berOID(o); return b }

// v2Agent test amaçlı v1/v2c ajanı.
func v2Agent(pc net.PacketConn, mib []mibEntry, community string) {
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
		pc.WriteTo(tlv(0x30, bytes.Join([][]byte{tlv(0x02, ver), tlv(0x04, c), respPDU(mib, tag, content)}, nil)), addr)
	}
}

// v3Agent test amaçlı SNMPv3 (USM) ajanı.
func v3Agent(t *testing.T, pc net.PacketConn, mib []mibEntry, a SNMPAuth) {
	eng := []byte{0x80, 0, 0x1f, 0x88, 0x80, 1, 2, 3, 4}
	srv, err := newSession(nil, a)
	if err != nil {
		t.Error(err)
		return
	}
	srv.engineID, srv.boots, srv.etime, srv.synced = eng, 7, 1000, time.Now()
	if srv.newHash != nil {
		srv.authKey = localizeKey(srv.newHash, passwordToKey(srv.newHash, a.AuthPass), eng)
		if srv.level == 3 {
			srv.privKey = localizeKey(srv.newHash, passwordToKey(srv.newHash, a.PrivPass), eng)[:16]
		}
	}
	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		raw := append([]byte{}, buf[:n]...)
		m, r, err := parseV3Raw(raw)
		if err != nil {
			continue
		}
		var out []byte
		if len(m.engineID) == 0 { // keşif
			rep := tlv(0xA8, bytes.Join([][]byte{berInt(1), berInt(0), berInt(0), tlv(0x30, tlv(0x30, append(mustOID("1.3.6.1.6.3.15.1.1.4.0"), tlv(0x41, []byte{1})...)))}, nil))
			sv := *srv
			sv.level = 0
			out = sv.buildV3(rep, false)
		} else {
			if srv.level >= 1 {
				cp := append([]byte{}, raw...)
				for i := 0; i < r.authLen; i++ {
					cp[r.authOff+i] = 0
				}
				mac := hmac.New(srv.newHash, srv.authKey)
				mac.Write(cp)
				if !hmac.Equal(mac.Sum(nil)[:srv.macLen], r.authParams) {
					rep := tlv(0xA8, bytes.Join([][]byte{berInt(1), berInt(0), berInt(0), tlv(0x30, tlv(0x30, append(mustOID("1.3.6.1.6.3.15.1.1.5.0"), tlv(0x41, []byte{1})...)))}, nil))
					sv := *srv
					sv.level = 0
					pc.WriteTo(sv.buildV3(rep, false), addr)
					continue
				}
			}
			if r.encrypted {
				plain, err := srv.decrypt(r.scopedOrEnc, r.privPrm, m.boots, m.etime)
				if err != nil {
					t.Errorf("ajan: %v", err)
					continue
				}
				_, sc, _, _ := readTLV(plain)
				m.fillPDU(sc)
			}
			out = srv.buildV3(respPDU(mib, m.pduTag, m.pdu), false)
		}
		pc.WriteTo(out, addr)
	}
}

func startAgent(t *testing.T, f func(pc net.PacketConn)) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go f(pc)
	return pc.LocalAddr().String()
}

func runHost(t *testing.T, target string, o Options) (Result, map[string]float64) {
	r := RunX(context.Background(), Spec{Type: "host", Target: target, Timeout: 5 * time.Second, Opts: o}, false)
	return r, r.Extra
}

func checkHost(t *testing.T, r Result, x map[string]float64) {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("hata: %v", r.Err)
	}
	want := map[string]float64{"host.cpu_pct": 30, "host.mem_pct": 40, "host.disk_pct": 93, "host.disk_pct./": 50, "host.disk_pct./var": 93}
	for k, v := range want {
		if got, ok := x[k]; !ok || got < v-0.01 || got > v+0.01 {
			t.Errorf("%s = %v (ok=%v), beklenen %v; tümü: %v", k, got, ok, v, x)
		}
	}
	if _, ok := x["host.disk_pct.Swap"]; ok {
		t.Error("takas alanı disk sayılmamalı")
	}
}

func TestHostV2c(t *testing.T) {
	addr := startAgent(t, func(pc net.PacketConn) { v2Agent(pc, fakeMIB(), "gizli") })
	r, x := runHost(t, addr, Options{Method: "snmp", Version: "2c", Community: "gizli"})
	checkHost(t, r, x)
	r, _ = runHost(t, addr, Options{Method: "snmp", Version: "2c", Community: "yanlis"})
	if r.Err == nil {
		t.Error("yanlış topluluk adı hata vermeli")
	}
}

func TestHostV3(t *testing.T) {
	cases := []SNMPAuth{
		{Version: "3", User: "u1"},
		{Version: "3", User: "u1", AuthProto: "MD5", AuthPass: "authpass123"},
		{Version: "3", User: "u1", AuthProto: "SHA", AuthPass: "authpass123", PrivProto: "DES", PrivPass: "privpass123"},
		{Version: "3", User: "u1", AuthProto: "SHA", AuthPass: "authpass123", PrivProto: "AES", PrivPass: "privpass123"},
		{Version: "3", User: "u1", AuthProto: "SHA256", AuthPass: "authpass123", PrivProto: "AES", PrivPass: "privpass123"},
		{Version: "3", User: "u1", AuthProto: "SHA512", AuthPass: "authpass123", PrivProto: "DES", PrivPass: "privpass123"},
	}
	for _, a := range cases {
		a := a
		t.Run(a.AuthProto+"/"+a.PrivProto, func(t *testing.T) {
			addr := startAgent(t, func(pc net.PacketConn) { v3Agent(t, pc, fakeMIB(), a) })
			o := Options{Method: "snmp", Version: "3", User: a.User, AuthProto: a.AuthProto, AuthPass: a.AuthPass, PrivProto: a.PrivProto, PrivPass: a.PrivPass}
			r, x := runHost(t, addr, o)
			checkHost(t, r, x)
			if a.AuthProto != "" {
				o.AuthPass = "yanlissifre1"
				if r, _ := runHost(t, addr, o); r.Err == nil {
					t.Error("yanlış kimlik doğrulama parolası hata vermeli")
				}
			}
		})
	}
}

func TestSNMPProbeV3Get(t *testing.T) {
	a := SNMPAuth{Version: "3", User: "u1", AuthProto: "SHA", AuthPass: "authpass123", PrivProto: "AES", PrivPass: "privpass123"}
	addr := startAgent(t, func(pc net.PacketConn) { v3Agent(t, pc, fakeMIB(), a) })
	r := RunX(context.Background(), Spec{Type: "snmp", Target: addr, Timeout: 5 * time.Second,
		Opts: Options{Version: "3", User: "u1", AuthProto: "SHA", AuthPass: "authpass123", PrivProto: "AES", PrivPass: "privpass123"}}, false)
	if r.Err != nil || r.Extra["snmp_value"] != 123456 {
		t.Fatalf("%v %v", r.Err, r.Extra)
	}
}

// TestServeFakeAgent elle deneme içindir: FAKE_SNMP_ADDR verilirse 10 dakika boyunca sahte bir SNMPv3 ajanı çalıştırır.
func TestServeFakeAgent(t *testing.T) {
	addr := os.Getenv("FAKE_SNMP_ADDR")
	if addr == "" {
		t.Skip()
	}
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go v3Agent(t, pc, fakeMIB(), SNMPAuth{Version: "3", User: "izle", AuthProto: "SHA", AuthPass: "authpass123", PrivProto: "AES", PrivPass: "privpass123"})
	time.Sleep(10 * time.Minute)
}
