package netflow

import (
	"encoding/binary"
	"net"
	"testing"
)

func u16(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }
func u32(v int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(v)) }
func cat(parts ...[]byte) []byte {
	var o []byte
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}

func v5Packet() []byte {
	h := cat(u16(5), u16(2), u32(1000), u32(1700000000), u32(0), u32(1), []byte{0, 0}, u16(1<<14|100)) // örnekleme 1/100
	rec := func(src, dst [4]byte, sp, dp int, proto byte, pk, by int) []byte {
		r := make([]byte, 48)
		copy(r[0:], src[:])
		copy(r[4:], dst[:])
		binary.BigEndian.PutUint32(r[16:], uint32(pk))
		binary.BigEndian.PutUint32(r[20:], uint32(by))
		binary.BigEndian.PutUint16(r[32:], uint16(sp))
		binary.BigEndian.PutUint16(r[34:], uint16(dp))
		r[38] = proto
		return r
	}
	return cat(h, rec([4]byte{10, 0, 0, 1}, [4]byte{10, 0, 0, 2}, 40000, 443, 6, 10, 15000), rec([4]byte{10, 0, 0, 3}, [4]byte{8, 8, 8, 8}, 5353, 53, 17, 1, 80))
}

func TestV5(t *testing.T) {
	d := NewDecoder()
	fl, err := d.Decode("1.1.1.1", v5Packet())
	if err != nil || len(fl) != 2 {
		t.Fatalf("v5: %v %d", err, len(fl))
	}
	f := fl[0]
	if !f.Src.Equal(net.IPv4(10, 0, 0, 1)) || f.DstPort != 443 || f.Proto != 6 || f.Bytes != 15000 || f.Packets != 10 || f.Sampling != 100 {
		t.Errorf("v5 alanları: %+v", f)
	}
}

func v9Template() []byte {
	fields := cat(u16(8), u16(4), u16(12), u16(4), u16(7), u16(2), u16(11), u16(2), u16(4), u16(1), u16(1), u16(4), u16(2), u16(4))
	tpl := cat(u16(256), u16(7), fields)
	fs := cat(u16(0), u16(4+len(tpl)), tpl)
	return cat(u16(9), u16(1), u32(1), u32(1700000000), u32(1), u32(77), fs)
}
func v9Data() []byte {
	rec := cat([]byte{192, 168, 1, 10}, []byte{172, 16, 0, 5}, u16(51000), u16(22), []byte{6}, u32(4096), u32(8))
	body := cat(rec, rec, []byte{0, 0}) // dolgu
	set := cat(u16(256), u16(4+len(body)), body)
	return cat(u16(9), u16(2), u32(1), u32(1700000000), u32(2), u32(77), set)
}

func TestV9TemplateThenData(t *testing.T) {
	d := NewDecoder()
	// şablondan önce veri: atılır, sayaç artar
	if fl, _ := d.Decode("r1", v9Data()); len(fl) != 0 || d.MissingTemplate.Load() != 1 {
		t.Fatalf("şablonsuz veri: %d %d", len(fl), d.MissingTemplate.Load())
	}
	if _, err := d.Decode("r1", v9Template()); err != nil {
		t.Fatal(err)
	}
	fl, err := d.Decode("r1", v9Data())
	if err != nil || len(fl) != 2 {
		t.Fatalf("v9: %v %d", err, len(fl))
	}
	if fl[0].Bytes != 4096 || fl[0].Packets != 8 || fl[0].DstPort != 22 || !fl[0].Dst.Equal(net.IPv4(172, 16, 0, 5)) {
		t.Errorf("v9 alanları: %+v", fl[0])
	}
	// başka ihracatçının şablonu paylaşılmamalı
	if fl, _ := d.Decode("r2", v9Data()); len(fl) != 0 {
		t.Error("şablon ihracatçılar arasında paylaşıldı")
	}
}

func ipfixTemplate() []byte {
	// srcv4, dstv4, bytes(8), pkts(8), proto, sport, dport, kurumsal alan(4B), değişken uzunluklu alan
	fields := cat(u16(8), u16(4), u16(12), u16(4), u16(1), u16(8), u16(2), u16(8), u16(4), u16(1), u16(7), u16(2), u16(11), u16(2),
		u16(0x8000|100), u16(4), u32(9999), u16(300), u16(0xffff))
	tpl := cat(u16(300), u16(9), fields)
	set := cat(u16(2), u16(4+len(tpl)), tpl)
	h := cat(u16(10), u16(16+len(set)), u32(1700000000), u32(1), u32(5))
	return cat(h, set)
}
func ipfixData() []byte {
	rec := cat([]byte{10, 1, 1, 1}, []byte{10, 2, 2, 2}, u32(0), u32(123456), u32(0), u32(99), []byte{17}, u16(1234), u16(53), u32(7), []byte{3, 'a', 'b', 'c'})
	set := cat(u16(300), u16(4+len(rec)*2), rec, rec)
	return cat(u16(10), u16(16+len(set)), u32(1700000000), u32(2), u32(5), set)
}

func TestIPFIXEnterpriseAndVariable(t *testing.T) {
	d := NewDecoder()
	d.Decode("r", ipfixTemplate())
	fl, err := d.Decode("r", ipfixData())
	if err != nil || len(fl) != 2 {
		t.Fatalf("ipfix: %v %d", err, len(fl))
	}
	if fl[1].Bytes != 123456 || fl[1].Packets != 99 || fl[1].Proto != 17 || fl[1].DstPort != 53 {
		t.Errorf("ipfix alanları: %+v", fl[1])
	}
}

func TestOptionsSampling(t *testing.T) {
	d := NewDecoder()
	// v9 seçenek şablonu: kapsam 4 bayt (sistem), seçenek: SAMPLING_INTERVAL(34, 4 bayt)
	opt := cat(u16(400), u16(4), u16(4), u16(1), u16(4), u16(34), u16(4))
	set := cat(u16(1), u16(4+len(opt)), opt)
	d.Decode("r", cat(u16(9), u16(1), u32(1), u32(1), u32(1), u32(3), set))
	data := cat(u32(0), u32(500))
	set = cat(u16(400), u16(4+len(data)), data)
	d.Decode("r", cat(u16(9), u16(1), u32(1), u32(1), u32(2), u32(3), set))
	d.Decode("r", v9Template()) // domain 77 değil 3; aynı alan için ayrı
	// domain 3 için veri şablonu
	fields := cat(u16(8), u16(4), u16(12), u16(4), u16(1), u16(4))
	tpl := cat(u16(256), u16(3), fields)
	tset := cat(u16(0), u16(4+len(tpl)), tpl)
	d.Decode("r", cat(u16(9), u16(1), u32(1), u32(1), u32(3), u32(3), tset))
	rec := cat([]byte{1, 1, 1, 1}, []byte{2, 2, 2, 2}, u32(1000))
	dset := cat(u16(256), u16(4+len(rec)), rec)
	fl, _ := d.Decode("r", cat(u16(9), u16(1), u32(1), u32(1), u32(4), u32(3), dset))
	if len(fl) != 1 || fl[0].Sampling != 500 {
		t.Fatalf("örnekleme: %+v", fl)
	}
}

// Bozuk/kesilmiş paketler hiçbir koşulda panik yaratmamalı.
func TestTruncatedNeverPanics(t *testing.T) {
	d := NewDecoder()
	d.Decode("r", v9Template())
	d.Decode("r", ipfixTemplate())
	for _, pkt := range [][]byte{v5Packet(), v9Template(), v9Data(), ipfixTemplate(), ipfixData()} {
		for i := 0; i <= len(pkt); i++ {
			d.Decode("r", pkt[:i])
		}
		// rastgele bozma
		for i := range pkt {
			b := append([]byte(nil), pkt...)
			b[i] ^= 0xff
			d.Decode("r", b)
		}
	}
	// sonsuz döngü koruması: sıfır uzunluklu alanlı şablon
	fields := cat(u16(8), u16(0), u16(12), u16(0))
	tpl := cat(u16(256), u16(2), fields)
	d.Decode("z", cat(u16(9), u16(1), u32(1), u32(1), u32(1), u32(1), cat(u16(0), u16(4+len(tpl)), tpl)))
	d.Decode("z", cat(u16(9), u16(1), u32(1), u32(1), u32(2), u32(1), cat(u16(256), u16(8), []byte{1, 2, 3, 4})))
}
