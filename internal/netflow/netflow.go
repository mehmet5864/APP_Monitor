// Package netflow NetFlow v5, v9 ve IPFIX (v10) paketlerini yalnızca standart kütüphaneyle çözer.
// Yalnızca akış özetini çıkarır: kaynak/hedef adres, port, protokol, bayt, paket ve örnekleme oranı.
package netflow

import (
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
)

// Flow tek bir akış kaydıdır.
type Flow struct {
	Src, Dst         net.IP
	SrcPort, DstPort uint16
	Proto            uint8
	Bytes, Packets   uint64
	Sampling         uint32 // 0 veya 1: örnekleme yok; N: her N pakette 1 örneklenmiş
}

// Stats çözücü sayaçlarıdır.
type Stats struct {
	Packets, Flows, MissingTemplate, Invalid atomic.Uint64
}

type tplKey struct {
	exp    string
	domain uint32
	id     uint16
}
type field struct {
	typ, length uint16
	ent         uint32
}
type template struct {
	fields  []field
	options bool
	fixed   int // değişken uzunluklu alan yoksa kayıt boyutu; varsa -1
	minRec  int
}

// Decoder şablonları ihracatçı + gözlem alanı bazında saklar.
type Decoder struct {
	mu   sync.Mutex
	tpl  map[tplKey]*template
	samp map[string]uint32
	Stats
}

const (
	maxTemplates = 20000
	maxFields    = 256
)

func NewDecoder() *Decoder { return &Decoder{tpl: map[tplKey]*template{}, samp: map[string]uint32{}} }

var errShort = errors.New("netflow: kısa veya bozuk paket")

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func beN(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// Decode bir UDP datagramını çözer. exporter, şablon anahtarı için ihracatçının kaynak adresidir.
func (d *Decoder) Decode(exporter string, pkt []byte) ([]Flow, error) {
	d.Packets.Add(1)
	if len(pkt) < 4 {
		d.Invalid.Add(1)
		return nil, errShort
	}
	var fl []Flow
	var err error
	switch be16(pkt) {
	case 5:
		fl, err = d.v5(pkt)
	case 9:
		fl, err = d.v9(exporter, pkt)
	case 10:
		fl, err = d.ipfix(exporter, pkt)
	default:
		err = errors.New("netflow: desteklenmeyen sürüm")
	}
	if err != nil {
		d.Invalid.Add(1)
	}
	d.Flows.Add(uint64(len(fl)))
	return fl, err
}

func (d *Decoder) v5(p []byte) ([]Flow, error) {
	if len(p) < 24 {
		return nil, errShort
	}
	n := int(be16(p[2:]))
	if n == 0 || n > 30 || 24+n*48 > len(p) {
		return nil, errShort
	}
	samp := uint32(be16(p[22:]) & 0x3fff)
	out := make([]Flow, 0, n)
	for i := 0; i < n; i++ {
		r := p[24+i*48 : 24+(i+1)*48]
		f := Flow{Src: net.IP(append([]byte(nil), r[0:4]...)), Dst: net.IP(append([]byte(nil), r[4:8]...)),
			Packets: uint64(be32(r[16:])), Bytes: uint64(be32(r[20:])), SrcPort: be16(r[32:]), DstPort: be16(r[34:]), Proto: r[38], Sampling: samp}
		if f.Bytes > 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

func (d *Decoder) put(k tplKey, t *template) {
	d.mu.Lock()
	if len(d.tpl) >= maxTemplates {
		d.tpl = map[tplKey]*template{}
	}
	d.tpl[k] = t
	d.mu.Unlock()
}
func (d *Decoder) get(k tplKey) *template {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tpl[k]
}

func finish(fs []field, options bool) *template {
	t := &template{fields: fs, options: options}
	for _, f := range fs {
		if f.length == 0xffff {
			t.fixed = -1
			t.minRec++
		} else if t.fixed >= 0 {
			t.fixed += int(f.length)
		}
		if f.length != 0xffff {
			t.minRec += int(f.length)
		}
	}
	return t
}

// ---- NetFlow v9 ----

func (d *Decoder) v9(exp string, p []byte) ([]Flow, error) {
	if len(p) < 20 {
		return nil, errShort
	}
	domain := be32(p[16:])
	var out []Flow
	off := 20
	for off+4 <= len(p) {
		id, ln := be16(p[off:]), int(be16(p[off+2:]))
		if ln < 4 || off+ln > len(p) {
			return out, errShort
		}
		body := p[off+4 : off+ln]
		switch {
		case id == 0:
			for q := 0; len(body)-q >= 4; {
				tid, fc := be16(body[q:]), int(be16(body[q+2:]))
				q += 4
				if fc == 0 || fc > maxFields || q+fc*4 > len(body) {
					break
				}
				fs := make([]field, fc)
				for i := range fs {
					fs[i] = field{typ: be16(body[q:]), length: be16(body[q+2:])}
					q += 4
				}
				if tid >= 256 {
					d.put(tplKey{exp, domain, tid}, finish(fs, false))
				}
			}
		case id == 1:
			for q := 0; len(body)-q >= 6; {
				tid, sl, ol := be16(body[q:]), int(be16(body[q+2:])), int(be16(body[q+4:]))
				q += 6
				n := (sl + ol) / 4
				if n == 0 || n > maxFields || q+n*4 > len(body) {
					break
				}
				fs := make([]field, n)
				for i := range fs {
					fs[i] = field{typ: be16(body[q:]), length: be16(body[q+2:])}
					q += 4
				}
				if tid >= 256 {
					d.put(tplKey{exp, domain, tid}, finish(fs, true))
				}
			}
		case id >= 256:
			out = append(out, d.data(exp, domain, id, body)...)
		}
		off += ln
	}
	return out, nil
}

// ---- IPFIX ----

func (d *Decoder) ipfix(exp string, p []byte) ([]Flow, error) {
	if len(p) < 16 {
		return nil, errShort
	}
	if l := int(be16(p[2:])); l >= 16 && l <= len(p) {
		p = p[:l]
	}
	domain := be32(p[12:])
	var out []Flow
	off := 16
	for off+4 <= len(p) {
		id, ln := be16(p[off:]), int(be16(p[off+2:]))
		if ln < 4 || off+ln > len(p) {
			return out, errShort
		}
		body := p[off+4 : off+ln]
		switch {
		case id == 2 || id == 3:
			for q := 0; len(body)-q >= 4; {
				tid, fc := be16(body[q:]), int(be16(body[q+2:]))
				q += 4
				if id == 3 {
					if len(body)-q < 2 {
						break
					}
					q += 2 // kapsam alanı sayısı (alanlar aynı biçimde okunur)
				}
				if fc == 0 || fc > maxFields {
					break
				}
				fs := make([]field, 0, fc)
				ok := true
				for i := 0; i < fc; i++ {
					if len(body)-q < 4 {
						ok = false
						break
					}
					f := field{typ: be16(body[q:]), length: be16(body[q+2:])}
					q += 4
					if f.typ&0x8000 != 0 {
						f.typ &= 0x7fff
						if len(body)-q < 4 {
							ok = false
							break
						}
						f.ent = be32(body[q:])
						q += 4
					}
					fs = append(fs, f)
				}
				if !ok {
					break
				}
				if tid >= 256 {
					d.put(tplKey{exp, domain, tid}, finish(fs, id == 3))
				}
			}
		case id >= 256:
			out = append(out, d.data(exp, domain, id, body)...)
		}
		off += ln
	}
	return out, nil
}

// data bir veri kümesindeki kayıtları şablona göre çözer.
func (d *Decoder) data(exp string, domain uint32, id uint16, body []byte) []Flow {
	t := d.get(tplKey{exp, domain, id})
	if t == nil {
		d.MissingTemplate.Add(1)
		return nil
	}
	var out []Flow
	ds := exp + "|" + strconv.FormatUint(uint64(domain), 10)
	d.mu.Lock()
	samp := d.samp[ds]
	d.mu.Unlock()
	p := 0
	for p < len(body) && len(body)-p >= max(t.minRec, 1) {
		start := p
		var f Flow
		var rs uint32
		for _, fd := range t.fields {
			l := int(fd.length)
			if fd.length == 0xffff {
				if p >= len(body) {
					return out
				}
				l = int(body[p])
				p++
				if l == 255 {
					if len(body)-p < 2 {
						return out
					}
					l = int(be16(body[p:]))
					p += 2
				}
			}
			if l < 0 || p+l > len(body) {
				return out
			}
			v := body[p : p+l]
			p += l
			if fd.ent != 0 {
				continue
			}
			switch fd.typ {
			case 1, 85: // IN_BYTES, octetTotalCount
				f.Bytes = beN(v)
			case 2, 86:
				f.Packets = beN(v)
			case 4:
				if l == 1 {
					f.Proto = v[0]
				}
			case 7:
				if l == 2 {
					f.SrcPort = be16(v)
				}
			case 11:
				if l == 2 {
					f.DstPort = be16(v)
				}
			case 8, 27:
				if l == 4 || l == 16 {
					f.Src = net.IP(append([]byte(nil), v...))
				}
			case 12, 28:
				if l == 4 || l == 16 {
					f.Dst = net.IP(append([]byte(nil), v...))
				}
			case 34, 305: // SAMPLING_INTERVAL, samplingPacketInterval
				rs = uint32(beN(v))
			}
		}
		if p == start {
			break
		}
		if t.options {
			if rs > 0 {
				d.mu.Lock()
				if len(d.samp) > 5000 {
					d.samp = map[string]uint32{}
				}
				d.samp[ds] = rs
				d.mu.Unlock()
				samp = rs
			}
			continue
		}
		if f.Src == nil || f.Dst == nil || f.Bytes == 0 {
			continue
		}
		f.Sampling = samp
		if rs > 0 {
			f.Sampling = rs
		}
		out = append(out, f)
	}
	return out
}
