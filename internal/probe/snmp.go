package probe

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Yaygın OID'ler: sysDescr 1.3.6.1.2.1.1.1.0, sysUpTime 1.3.6.1.2.1.1.3.0, ifNumber 1.3.6.1.2.1.2.1.0
const defaultOID = "1.3.6.1.2.1.1.3.0"

var oidRe = regexp.MustCompile(`^[0-2](\.[0-9]+){2,}$`)

// ValidOID noktalı OID biçimini denetler.
func ValidOID(s string) bool { return len(s) <= 128 && oidRe.MatchString(s) }

func berLen(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	if n < 256 {
		return []byte{0x81, byte(n)}
	}
	return []byte{0x82, byte(n >> 8), byte(n)}
}

func tlv(tag byte, content []byte) []byte {
	out := append([]byte{tag}, berLen(len(content))...)
	return append(out, content...)
}

func berInt(v int64) []byte {
	b := []byte{}
	for {
		b = append([]byte{byte(v)}, b...)
		v >>= 8
		if (v == 0 && b[0]&0x80 == 0) || (v == -1 && b[0]&0x80 != 0) {
			break
		}
	}
	return tlv(0x02, b)
}

func berOID(oid string) ([]byte, error) {
	parts := strings.Split(oid, ".")
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, errors.New("geçersiz OID")
		}
		nums[i] = n
	}
	if nums[0] > 2 || (nums[0] < 2 && nums[1] > 39) {
		return nil, errors.New("geçersiz OID")
	}
	body := []byte{byte(nums[0]*40 + nums[1])}
	for _, n := range nums[2:] {
		var enc []byte
		enc = append(enc, byte(n&0x7f))
		for n >>= 7; n > 0; n >>= 7 {
			enc = append([]byte{byte(n&0x7f) | 0x80}, enc...)
		}
		body = append(body, enc...)
	}
	return tlv(0x06, body), nil
}

func readTLV(b []byte) (tag byte, val, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, errors.New("kısa SNMP yanıtı")
	}
	tag = b[0]
	l, off := int(b[1]), 2
	if l&0x80 != 0 {
		nb := l & 0x7f
		if nb == 0 || nb > 3 || len(b) < 2+nb {
			return 0, nil, nil, errors.New("geçersiz SNMP uzunluğu")
		}
		l = 0
		for i := 0; i < nb; i++ {
			l = l<<8 | int(b[2+i])
		}
		off = 2 + nb
	}
	if len(b) < off+l {
		return 0, nil, nil, errors.New("eksik SNMP yanıtı")
	}
	return tag, b[off : off+l], b[off+l:], nil
}

func snmpGetRequest(version int, community, oid string, reqID int64) ([]byte, error) {
	o, err := berOID(oid)
	if err != nil {
		return nil, err
	}
	vb := tlv(0x30, append(o, 0x05, 0x00))
	pdu := tlv(0xA0, append(append(append(berInt(reqID), berInt(0)...), berInt(0)...), tlv(0x30, vb)...))
	msg := append(append(berInt(int64(version)), tlv(0x04, []byte(community))...), pdu...)
	return tlv(0x30, msg), nil
}

// snmpDecode GET yanıtından değeri çıkarır: metin gösterimi ve (sayısalsa) sayı.
func snmpDecode(resp []byte, reqID int64) (text string, num float64, isNum bool, err error) {
	tag, body, _, err := readTLV(resp)
	if err != nil || tag != 0x30 {
		return "", 0, false, errors.New("geçersiz SNMP yanıtı")
	}
	_, _, body, err = readTLV(body) // sürüm
	if err != nil {
		return
	}
	_, _, body, err = readTLV(body) // topluluk
	if err != nil {
		return
	}
	tag, pdu, _, err := readTLV(body)
	if err != nil || tag != 0xA2 {
		return "", 0, false, errors.New("SNMP GetResponse alınamadı")
	}
	_, idv, pdu, err := readTLV(pdu)
	if err != nil {
		return
	}
	if new(big.Int).SetBytes(idv).Int64() != reqID {
		return "", 0, false, errors.New("SNMP istek kimliği uyuşmuyor")
	}
	_, es, pdu, err := readTLV(pdu)
	if err != nil {
		return
	}
	if st := new(big.Int).SetBytes(es).Int64(); st != 0 {
		names := map[int64]string{1: "tooBig", 2: "noSuchName", 3: "badValue", 4: "readOnly", 5: "genErr", 6: "noAccess"}
		n := names[st]
		if n == "" {
			n = strconv.FormatInt(st, 10)
		}
		return "", 0, false, fmt.Errorf("SNMP hata durumu: %s", n)
	}
	_, _, pdu, err = readTLV(pdu) // hata dizini
	if err != nil {
		return
	}
	_, vbl, _, err := readTLV(pdu)
	if err != nil {
		return
	}
	_, vb, _, err := readTLV(vbl)
	if err != nil {
		return
	}
	_, _, vb, err = readTLV(vb) // OID
	if err != nil {
		return
	}
	vt, v, _, err := readTLV(vb)
	if err != nil {
		return
	}
	switch vt {
	case 0x80, 0x81, 0x82:
		return "", 0, false, errors.New("OID bulunamadı (noSuchObject/noSuchInstance)")
	case 0x02: // INTEGER (işaretli)
		n := new(big.Int).SetBytes(v)
		if len(v) > 0 && v[0]&0x80 != 0 {
			n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(v))))
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return n.String(), f, true, nil
	case 0x41, 0x42, 0x43, 0x46: // Counter32, Gauge32, TimeTicks, Counter64
		n := new(big.Int).SetBytes(v)
		f, _ := new(big.Float).SetInt(n).Float64()
		return n.String(), f, true, nil
	case 0x04:
		return string(v), 0, false, nil
	case 0x40:
		if len(v) == 4 {
			return net.IP(v).String(), 0, false, nil
		}
	}
	return fmt.Sprintf("%x", v), 0, false, nil
}

// snmpProbe SNMP v1/v2c GET gönderir. SNMPv3 desteklenmez. Sayısal değer snmp_value metriği olur.
func snmpProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	host, port, err := splitTarget(spec.Target, 161)
	if err != nil {
		return nil, err
	}
	ip, err := resolveIP(ctx, host, block)
	if err != nil {
		return nil, err
	}
	community := spec.Opts.Community
	if community == "" {
		community = "public"
	}
	oid := spec.Opts.OID
	if oid == "" {
		oid = defaultOID
	}
	version := 1 // v2c
	if spec.Opts.Version == "1" {
		version = 0
	}
	reqID := time.Now().UnixNano() & 0x3fffffff
	pkt, err := snmpGetRequest(version, community, oid, reqID)
	if err != nil {
		return nil, err
	}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if _, err := conn.Write(pkt); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, errors.New("SNMP yanıtı yok (topluluk adı, sürüm veya erişim izni yanlış olabilir)")
	}
	text, num, isNum, err := snmpDecode(buf[:n], reqID)
	if err != nil {
		return nil, err
	}
	if e := spec.Opts.Expect; e != "" && !strings.Contains(text, e) {
		return nil, fmt.Errorf("SNMP değeri %q beklenen %q içermiyor", truncate(text, 60), e)
	}
	if isNum {
		return map[string]float64{"snmp_value": num}, nil
	}
	return nil, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
