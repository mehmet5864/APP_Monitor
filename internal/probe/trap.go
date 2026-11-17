package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Trap alınan bir SNMP v1/v2c trap'inin özetidir.
type Trap struct {
	Version   int
	Community string
	OID       string // v2c: snmpTrapOID.0; v1: enterprise (+ genel/özel tür)
	Uptime    int64
	Vars      []TrapVar
}

// TrapVar bir değişken bağlamasıdır.
type TrapVar struct{ OID, Value string }

func berInteger(b []byte) int64 {
	var v int64
	if len(b) > 0 && b[0]&0x80 != 0 {
		v = -1
	}
	for _, c := range b {
		v = v<<8 | int64(c)
	}
	return v
}

func varText(tag byte, v []byte) string {
	switch tag {
	case 0x02, 0x41, 0x42, 0x43, 0x46:
		if tag == 0x02 {
			return fmt.Sprint(berInteger(v))
		}
		var u uint64
		for _, c := range v {
			u = u<<8 | uint64(c)
		}
		return fmt.Sprint(u)
	case 0x04:
		return truncate(strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return '.'
			}
			return r
		}, string(v)), 200)
	case 0x06:
		return oidString(v)
	case 0x40:
		if len(v) == 4 {
			return net.IP(v).String()
		}
	}
	return ""
}

// ParseTrap v1 (Trap-PDU) ve v2c (SNMPv2-Trap / Inform) paketlerini çözer.
func ParseTrap(pkt []byte) (Trap, error) {
	var t Trap
	_, body, _, err := readTLV(pkt)
	if err != nil {
		return t, err
	}
	_, ver, body, err := readTLV(body)
	if err != nil {
		return t, err
	}
	t.Version = int(berInteger(ver))
	if t.Version != 0 && t.Version != 1 {
		return t, errors.New("desteklenmeyen SNMP sürümü (yalnızca v1/v2c)")
	}
	_, comm, body, err := readTLV(body)
	if err != nil {
		return t, err
	}
	t.Community = string(comm)
	tag, pdu, _, err := readTLV(body)
	if err != nil {
		return t, err
	}
	var vbl []byte
	switch tag {
	case 0xA4: // v1 Trap-PDU
		_, ent, r, err := readTLV(pdu)
		if err != nil {
			return t, err
		}
		_, _, r, _ = readTLV(r) // agent-addr
		_, gen, r, _ := readTLV(r)
		_, spec, r, _ := readTLV(r)
		_, up, r, _ := readTLV(r)
		t.Uptime = berInteger(up)
		t.OID = oidString(ent)
		if g := berInteger(gen); g != 6 {
			t.OID += fmt.Sprintf(" (genel tür %d)", g)
		} else {
			t.OID += fmt.Sprintf(".0.%d", berInteger(spec))
		}
		_, vbl, _, err = readTLV(r)
		if err != nil {
			return t, err
		}
	case 0xA7, 0xA6: // v2 Trap / Inform
		_, _, r, err := readTLV(pdu) // request-id
		if err != nil {
			return t, err
		}
		_, _, r, _ = readTLV(r)
		_, _, r, _ = readTLV(r)
		_, vbl, _, err = readTLV(r)
		if err != nil {
			return t, err
		}
	default:
		return t, errors.New("trap PDU'su değil")
	}
	for len(vbl) > 0 && len(t.Vars) < 32 {
		_, vb, rest, err := readTLV(vbl)
		if err != nil {
			break
		}
		vbl = rest
		_, o, vr, err := readTLV(vb)
		if err != nil {
			break
		}
		vt, vv, _, err := readTLV(vr)
		if err != nil {
			break
		}
		oid, val := oidString(o), varText(vt, vv)
		switch {
		case oid == "1.3.6.1.2.1.1.3.0" && t.Version == 1:
			var u int64
			for _, c := range vv {
				u = u<<8 | int64(c)
			}
			t.Uptime = u
		case oid == "1.3.6.1.6.3.1.1.4.1.0" && vt == 0x06:
			t.OID = val
		default:
			t.Vars = append(t.Vars, TrapVar{oid, val})
		}
	}
	return t, nil
}

// ListenTraps UDP üzerinde trap dinler; her geçerli paket için fn çağrılır. ctx bitince kapanır.
func ListenTraps(ctx context.Context, addr string, fn func(src net.IP, t Trap)) (net.Addr, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	go func() { <-ctx.Done(); pc.Close() }()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			t, err := ParseTrap(buf[:n])
			if err != nil {
				continue
			}
			if ua, ok := from.(*net.UDPAddr); ok {
				fn(ua.IP, t)
			}
		}
	}()
	return pc.LocalAddr(), nil
}
