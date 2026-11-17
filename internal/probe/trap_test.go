package probe

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func trapOID(t *testing.T, s string) []byte {
	b, err := berOID(s)
	if err != nil {
		t.Fatal(err)
	}
	_, v, _, _ := readTLV(b)
	return v
}

func TestParseTrapV2cAndListen(t *testing.T) {
	vb := func(oid string, tag byte, v []byte) []byte {
		return tlv(0x30, bytes.Join([][]byte{tlv(0x06, trapOID(t, oid)), tlv(tag, v)}, nil))
	}
	vbl := tlv(0x30, bytes.Join([][]byte{
		vb("1.3.6.1.2.1.1.3.0", 0x43, []byte{0x01, 0x00}),
		vb("1.3.6.1.6.3.1.1.4.1.0", 0x06, trapOID(t, "1.3.6.1.6.3.1.1.5.3")), // linkDown
		vb("1.3.6.1.2.1.2.2.1.1.2", 0x02, []byte{2}),
	}, nil))
	pdu := tlv(0xA7, bytes.Join([][]byte{tlv(0x02, []byte{7}), tlv(0x02, []byte{0}), tlv(0x02, []byte{0}), vbl}, nil))
	pkt := tlv(0x30, bytes.Join([][]byte{tlv(0x02, []byte{1}), tlv(0x04, []byte("trapkom")), pdu}, nil))
	tr, err := ParseTrap(pkt)
	if err != nil || tr.OID != "1.3.6.1.6.3.1.1.5.3" || tr.Community != "trapkom" || len(tr.Vars) != 1 || tr.Vars[0].Value != "2" {
		t.Fatalf("%+v %v", tr, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Trap, 1)
	addr, err := ListenTraps(ctx, "127.0.0.1:0", func(src net.IP, t Trap) { got <- t })
	if err != nil {
		t.Fatal(err)
	}
	c, _ := net.Dial("udp", addr.String())
	c.Write([]byte("çöp paket"))
	c.Write(pkt)
	select {
	case g := <-got:
		if g.OID != tr.OID {
			t.Fatal(g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trap alınamadı")
	}
}

func TestParseTrapV1(t *testing.T) {
	pdu := tlv(0xA4, bytes.Join([][]byte{tlv(0x06, trapOID(t, "1.3.6.1.4.1.9999")), tlv(0x40, []byte{10, 0, 0, 5}), tlv(0x02, []byte{6}), tlv(0x02, []byte{42}), tlv(0x43, []byte{5}), tlv(0x30, nil)}, nil))
	pkt := tlv(0x30, bytes.Join([][]byte{tlv(0x02, []byte{0}), tlv(0x04, []byte("c")), pdu}, nil))
	tr, err := ParseTrap(pkt)
	if err != nil || tr.OID != "1.3.6.1.4.1.9999.0.42" || tr.Version != 0 {
		t.Fatalf("%+v %v", tr, err)
	}
}
