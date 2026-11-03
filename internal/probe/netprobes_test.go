package probe

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func udpServer(t *testing.T, handle func(req []byte) []byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if r := handle(buf[:n]); r != nil {
				pc.WriteTo(r, addr)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func runX(spec Spec) Result {
	spec.Timeout = 2 * time.Second
	return RunX(context.Background(), spec, false)
}

func TestSNMP(t *testing.T) {
	// Sahte aracı: gelen isteğin kimliğini okuyup TimeTicks=123456 döndürür.
	addr := udpServer(t, func(req []byte) []byte {
		_, body, _, _ := readTLV(req)
		_, _, body, _ = readTLV(body)
		_, comm, body, _ := readTLV(body)
		if string(comm) != "gizli" {
			return nil
		}
		_, pdu, _, _ := readTLV(body)
		_, id, _, _ := readTLV(pdu)
		o, _ := berOID("1.3.6.1.2.1.1.3.0")
		val := tlv(0x43, []byte{0x01, 0xE2, 0x40})
		vb := tlv(0x30, tlv(0x30, append(o, val...)))
		resp := tlv(0xA2, append(append(append(tlv(0x02, id), berInt(0)...), berInt(0)...), vb...))
		return tlv(0x30, append(append(berInt(1), tlv(0x04, comm)...), resp...))
	})
	r := runX(Spec{Type: "snmp", Target: addr, Opts: Options{Community: "gizli"}})
	if !r.Up || r.Extra["snmp_value"] != 123456 {
		t.Fatalf("snmp başarılı olmalı: %+v err=%v", r, r.Err)
	}
	if r := runX(Spec{Type: "snmp", Target: addr, Opts: Options{Community: "yanlis"}}); r.Up || r.Err == nil {
		t.Fatal("yanlış topluluk başarısız olmalı")
	}
	if r := runX(Spec{Type: "snmp", Target: addr, Opts: Options{Community: "gizli", OID: "abc"}}); r.Up {
		t.Fatal("geçersiz OID reddedilmeli")
	}
}

func TestNTP(t *testing.T) {
	addr := udpServer(t, func(req []byte) []byte {
		resp := make([]byte, 48)
		resp[0], resp[1] = 0x1C, 2
		now := time.Now().Add(2 * time.Second) // sunucu 2 sn ileride
		sec := uint32(now.Unix() + ntpEpoch)
		binary.BigEndian.PutUint32(resp[32:], sec)
		binary.BigEndian.PutUint32(resp[40:], sec)
		return resp
	})
	r := runX(Spec{Type: "ntp", Target: addr})
	if !r.Up || r.Extra["ntp_offset_ms"] < 1000 || r.Extra["ntp_offset_ms"] > 3500 {
		t.Fatalf("ntp sapması ~2000 ms olmalı: %+v %v", r.Extra, r.Err)
	}
}

func TestUDPAndTCPExpect(t *testing.T) {
	addr := udpServer(t, func(req []byte) []byte { return append([]byte("pong:"), req...) })
	if r := runX(Spec{Type: "udp", Target: addr, Opts: Options{Send: "ping", Expect: "pong:ping"}}); !r.Up {
		t.Fatalf("udp: %v", r.Err)
	}
	if r := runX(Spec{Type: "udp", Target: addr, Opts: Options{Send: "ping", Expect: "yok"}}); r.Up {
		t.Fatal("udp beklenen metin yokken başarısız olmalı")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("SSH-2.0-test\r\n"))
			c.Close()
		}
	}()
	if r := runX(Spec{Type: "tcp", Target: ln.Addr().String(), Opts: Options{Expect: "SSH-"}}); !r.Up {
		t.Fatalf("tcp banner: %v", r.Err)
	}
	if r := runX(Spec{Type: "tcp", Target: ln.Addr().String(), Opts: Options{Expect: "SMTP"}}); r.Up {
		t.Fatal("tcp yanlış banner başarısız olmalı")
	}
}

func TestTLSCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	if r := runX(Spec{Type: "tls", Target: host}); r.Up { // güvenilmeyen sertifika: doğrulama başarısız olmalı
		t.Fatal("güvenilmeyen sertifika reddedilmeli")
	}
	r := runX(Spec{Type: "tls", Target: host, HTTP: HTTPOptions{SkipTLSVerify: true}})
	if !r.Up || r.Extra["cert_days_left"] < 300 {
		t.Fatalf("sertifika kalan gün ölçülmeli: %+v %v", r.Extra, r.Err)
	}
}

func TestICMP(t *testing.T) {
	r := runX(Spec{Type: "icmp", Target: "127.0.0.1", Opts: Options{Count: 2}})
	if r.Err != nil && strings.Contains(r.Err.Error(), "soketi açılamadı") {
		t.Skip("ICMP için yetki yok: " + r.Err.Error())
	}
	if !r.Up || r.Extra["packet_loss_pct"] != 0 {
		t.Fatalf("loopback ping başarılı olmalı: %+v %v", r.Extra, r.Err)
	}
}

func TestSaaSBlocksPrivate(t *testing.T) {
	for _, typ := range []string{"icmp", "snmp", "udp", "ntp"} {
		target := "127.0.0.1"
		if typ == "udp" {
			target = "127.0.0.1:9"
		}
		if r := RunX(context.Background(), Spec{Type: typ, Target: target, Timeout: time.Second}, true); r.Up || r.Err == nil {
			t.Fatalf("%s: SaaS modunda özel adres engellenmeli", typ)
		}
	}
}

func TestDNSBadRecord(t *testing.T) {
	if r := runX(Spec{Type: "dns", Target: "ornek.test", Opts: Options{Record: "XYZ"}}); r.Up {
		t.Fatal("geçersiz kayıt türü reddedilmeli")
	}
}
