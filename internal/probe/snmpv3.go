package probe

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"
)

// SNMP istemcisi: v1, v2c ve v3 (USM, RFC 3414 / 3826 / 7860). Yalnızca Go standart kütüphanesi kullanılır.
// Desteklenen kimlik doğrulama: MD5, SHA (SHA-1), SHA-224/256/384/512. Gizlilik: DES ve AES-128.

// SNMPAuth bir SNMP oturumunun kimlik bilgileridir.
type SNMPAuth struct {
	Version   string // "1", "2c" veya "3"
	Community string
	User      string
	AuthProto string // "", MD5, SHA, SHA224, SHA256, SHA384, SHA512
	AuthPass  string
	PrivProto string // "", DES, AES
	PrivPass  string
	Context   string
}

// snmpVal bir varbind değeridir.
type snmpVal struct {
	OID  string
	Type byte
	Raw  []byte
}

var errSNMPEnd = errors.New("MIB sonu")

func (v snmpVal) Num() (float64, bool) {
	switch v.Type {
	case 0x02:
		n := new(big.Int).SetBytes(v.Raw)
		if len(v.Raw) > 0 && v.Raw[0]&0x80 != 0 {
			n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(v.Raw))))
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	case 0x41, 0x42, 0x43, 0x46:
		f, _ := new(big.Float).SetInt(new(big.Int).SetBytes(v.Raw)).Float64()
		return f, true
	}
	return 0, false
}

func (v snmpVal) Text() string {
	switch v.Type {
	case 0x04:
		return string(v.Raw)
	case 0x06:
		return oidString(v.Raw)
	}
	if f, ok := v.Num(); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%x", v.Raw)
}

func oidString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	parts := []string{strconv.Itoa(int(b[0]) / 40), strconv.Itoa(int(b[0]) % 40)}
	var n uint64
	for _, c := range b[1:] {
		n = n<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			parts = append(parts, strconv.FormatUint(n, 10))
			n = 0
		}
	}
	return strings.Join(parts, ".")
}

func parseInt(b []byte) int64 {
	n := new(big.Int).SetBytes(b)
	if len(b) > 0 && b[0]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
	}
	return n.Int64()
}

type snmpSession struct {
	conn   net.Conn
	a      SNMPAuth
	nextID int64

	engineID     []byte
	boots, etime int64
	synced       time.Time
	authKey      []byte
	privKey      []byte
	newHash      func() hash.Hash
	macLen       int
	desCounter   uint32
	aesSalt      uint64
	level        byte // 0 noAuthNoPriv, 1 authNoPriv, 3 authPriv (bayrak değeri)
}

func authHash(proto string) (func() hash.Hash, int, error) {
	switch strings.ToUpper(proto) {
	case "MD5":
		return md5.New, 12, nil
	case "SHA", "SHA1", "SHA-1":
		return sha1.New, 12, nil
	case "SHA224":
		return sha256.New224, 16, nil
	case "SHA256":
		return sha256.New, 24, nil
	case "SHA384":
		return sha512.New384, 32, nil
	case "SHA512":
		return sha512.New, 48, nil
	}
	return nil, 0, errors.New("desteklenmeyen SNMPv3 kimlik doğrulama protokolü: " + proto)
}

// passwordToKey RFC 3414 A.2.1: parolayı 1 MB'a genişletip özetler.
func passwordToKey(h func() hash.Hash, pass string) []byte {
	d := h()
	p := []byte(pass)
	buf := make([]byte, 64)
	idx := 0
	for i := 0; i < 1048576/64; i++ {
		for j := range buf {
			buf[j] = p[idx%len(p)]
			idx++
		}
		d.Write(buf)
	}
	return d.Sum(nil)
}

// localizeKey RFC 3414 A.2.2: anahtarı motor kimliğine bağlar.
func localizeKey(h func() hash.Hash, ku, engineID []byte) []byte {
	d := h()
	d.Write(ku)
	d.Write(engineID)
	d.Write(ku)
	return d.Sum(nil)
}

func newSession(conn net.Conn, a SNMPAuth) (*snmpSession, error) {
	s := &snmpSession{conn: conn, a: a, nextID: time.Now().UnixNano() & 0xfffff}
	if a.Version != "3" {
		return s, nil
	}
	if a.User == "" {
		return nil, errors.New("SNMPv3 için kullanıcı adı zorunlu")
	}
	if a.AuthProto != "" {
		var err error
		if s.newHash, s.macLen, err = authHash(a.AuthProto); err != nil {
			return nil, err
		}
		if len(a.AuthPass) < 8 {
			return nil, errors.New("SNMPv3 kimlik doğrulama parolası en az 8 karakter olmalı")
		}
		s.level = 1
	}
	if a.PrivProto != "" {
		if s.newHash == nil {
			return nil, errors.New("SNMPv3 gizlilik için kimlik doğrulama da gerekir")
		}
		switch strings.ToUpper(a.PrivProto) {
		case "DES", "AES", "AES128":
		default:
			return nil, errors.New("desteklenmeyen SNMPv3 gizlilik protokolü: " + a.PrivProto)
		}
		if len(a.PrivPass) < 8 {
			return nil, errors.New("SNMPv3 gizlilik parolası en az 8 karakter olmalı")
		}
		s.level = 3
	}
	var seed [8]byte
	rand.Read(seed[:])
	s.aesSalt = binary.BigEndian.Uint64(seed[:])
	s.desCounter = binary.BigEndian.Uint32(seed[:4])
	return s, nil
}

func (s *snmpSession) id() int64 {
	s.nextID = (s.nextID + 1) & 0x3fffffff
	if s.nextID == 0 {
		s.nextID = 1
	}
	return s.nextID
}

func buildPDU(tag byte, reqID int64, oid string) ([]byte, error) {
	var vbl []byte
	if oid != "" {
		o, err := berOID(oid)
		if err != nil {
			return nil, err
		}
		vbl = tlv(0x30, append(o, 0x05, 0x00))
	}
	body := append(append(append(berInt(reqID), berInt(0)...), berInt(0)...), tlv(0x30, vbl)...)
	return tlv(tag, body), nil
}

func (s *snmpSession) exchange(pkt []byte) ([]byte, error) {
	if _, err := s.conn.Write(pkt); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	n, err := s.conn.Read(buf)
	if err != nil {
		return nil, errors.New("SNMP yanıtı yok (adres, port, kimlik bilgisi veya erişim izni yanlış olabilir)")
	}
	return buf[:n], nil
}

func (s *snmpSession) discover() error {
	pdu, _ := buildPDU(0xA0, s.id(), "")
	pkt := s.buildV3(pdu, true)
	resp, err := s.exchange(pkt)
	if err != nil {
		return err
	}
	m, err := parseV3(resp)
	if err != nil {
		return err
	}
	if len(m.engineID) == 0 {
		return errors.New("SNMPv3 motor kimliği alınamadı")
	}
	s.engineID, s.boots, s.etime, s.synced = m.engineID, m.boots, m.etime, time.Now()
	if s.newHash != nil {
		ku := passwordToKey(s.newHash, s.a.AuthPass)
		s.authKey = localizeKey(s.newHash, ku, s.engineID)
		if s.level == 3 {
			kp := passwordToKey(s.newHash, s.a.PrivPass)
			lp := localizeKey(s.newHash, kp, s.engineID)
			if len(lp) < 16 {
				return errors.New("gizlilik anahtarı çok kısa")
			}
			s.privKey = lp[:16]
		}
	}
	return nil
}

func (s *snmpSession) curTime() int64 { return s.etime + int64(time.Since(s.synced).Seconds()) }

// buildV3 SNMPv3 mesajı kurar. discover=true ise kimlik doğrulamasız keşif mesajıdır.
func (s *snmpSession) buildV3(pdu []byte, discover bool) []byte {
	level := s.level
	engine := s.engineID
	var boots, etime int64
	user := []byte(s.a.User)
	if discover {
		level, engine, user = 0, nil, nil
	} else {
		boots, etime = s.boots, s.curTime()
	}
	flags := byte(0x04)
	if level >= 1 {
		flags |= 0x01
	}
	if level == 3 {
		flags |= 0x02
	}
	scoped := tlv(0x30, append(append(tlv(0x04, engine), tlv(0x04, []byte(s.a.Context))...), pdu...))
	msgData := scoped
	var privParams []byte
	if level == 3 {
		privParams, msgData = s.encrypt(scoped, boots, etime)
		msgData = tlv(0x04, msgData)
	}
	build := func(auth []byte) []byte {
		sp := tlv(0x30, bytes.Join([][]byte{tlv(0x04, engine), berInt(boots), berInt(etime), tlv(0x04, user), tlv(0x04, auth), tlv(0x04, privParams)}, nil))
		hdr := tlv(0x30, bytes.Join([][]byte{berInt(s.id()), berInt(65507), tlv(0x04, []byte{flags}), berInt(3)}, nil))
		return tlv(0x30, bytes.Join([][]byte{berInt(3), hdr, tlv(0x04, sp), msgData}, nil))
	}
	if level < 1 {
		return build(nil)
	}
	// msgID her çağrıda değişeceği için sabitle
	saved := s.nextID
	zero := build(make([]byte, s.macLen))
	s.nextID = saved
	mac := hmac.New(s.newHash, s.authKey)
	mac.Write(zero)
	return build(mac.Sum(nil)[:s.macLen])
}

func (s *snmpSession) encrypt(plain []byte, boots, etime int64) (privParams, enc []byte) {
	if strings.ToUpper(s.a.PrivProto) == "DES" {
		salt := make([]byte, 8)
		binary.BigEndian.PutUint32(salt[:4], uint32(boots))
		s.desCounter++
		binary.BigEndian.PutUint32(salt[4:], s.desCounter)
		iv := make([]byte, 8)
		for i := range iv {
			iv[i] = s.privKey[8+i] ^ salt[i]
		}
		if r := len(plain) % 8; r != 0 {
			plain = append(append([]byte{}, plain...), make([]byte, 8-r)...)
		}
		blk, _ := des.NewCipher(s.privKey[:8])
		out := make([]byte, len(plain))
		cipher.NewCBCEncrypter(blk, iv).CryptBlocks(out, plain)
		return salt, out
	}
	salt := make([]byte, 8)
	s.aesSalt++
	binary.BigEndian.PutUint64(salt, s.aesSalt)
	iv := aesIV(boots, etime, salt)
	blk, _ := aes.NewCipher(s.privKey)
	out := make([]byte, len(plain))
	cipher.NewCFBEncrypter(blk, iv).XORKeyStream(out, plain)
	return salt, out
}

func aesIV(boots, etime int64, salt []byte) []byte {
	iv := make([]byte, 16)
	binary.BigEndian.PutUint32(iv[0:4], uint32(boots))
	binary.BigEndian.PutUint32(iv[4:8], uint32(etime))
	copy(iv[8:], salt)
	return iv
}

type v3msg struct {
	flags        byte
	engineID     []byte
	boots, etime int64
	pduTag       byte
	pdu          []byte
}

// parseV3 yanıtı çözer; kimlik doğrulama/şifre çözme oturum bağlamı gerektirdiğinden ham alanları döndürür.
func parseV3(raw []byte) (*v3msg, error) {
	m, _, err := parseV3Raw(raw)
	return m, err
}

type v3raw struct {
	msg                 []byte
	authOff, authLen    int
	authParams, privPrm []byte
	scopedOrEnc         []byte
	encrypted           bool
}

func parseV3Raw(raw []byte) (*v3msg, *v3raw, error) {
	tag, body, _, err := readTLV(raw)
	if err != nil || tag != 0x30 {
		return nil, nil, errors.New("geçersiz SNMPv3 yanıtı")
	}
	_, ver, body, err := readTLV(body)
	if err != nil || parseInt(ver) != 3 {
		return nil, nil, errors.New("SNMPv3 yanıtı bekleniyordu (sürüm uyuşmuyor)")
	}
	_, hdr, body, err := readTLV(body)
	if err != nil {
		return nil, nil, err
	}
	_, _, hdr, err = readTLV(hdr) // msgID
	if err != nil {
		return nil, nil, err
	}
	_, _, hdr, err = readTLV(hdr) // maxSize
	if err != nil {
		return nil, nil, err
	}
	_, fl, _, err := readTLV(hdr)
	if err != nil || len(fl) < 1 {
		return nil, nil, errors.New("geçersiz SNMPv3 bayrakları")
	}
	_, spo, body, err := readTLV(body)
	if err != nil {
		return nil, nil, err
	}
	tag, sp, _, err := readTLV(spo)
	if err != nil || tag != 0x30 {
		return nil, nil, errors.New("geçersiz SNMPv3 güvenlik parametreleri")
	}
	m := &v3msg{flags: fl[0]}
	r := &v3raw{msg: raw}
	_, m.engineID, sp, err = readTLV(sp)
	if err != nil {
		return nil, nil, err
	}
	var v []byte
	_, v, sp, err = readTLV(sp)
	if err != nil {
		return nil, nil, err
	}
	m.boots = parseInt(v)
	_, v, sp, err = readTLV(sp)
	if err != nil {
		return nil, nil, err
	}
	m.etime = parseInt(v)
	_, _, sp, err = readTLV(sp) // kullanıcı adı
	if err != nil {
		return nil, nil, err
	}
	_, r.authParams, sp, err = readTLV(sp)
	if err != nil {
		return nil, nil, err
	}
	r.authOff = cap(raw) - cap(r.authParams)
	r.authLen = len(r.authParams)
	_, r.privPrm, _, err = readTLV(sp)
	if err != nil {
		return nil, nil, err
	}
	dt, data, _, err := readTLV(body)
	if err != nil {
		return nil, nil, err
	}
	r.encrypted = dt == 0x04
	r.scopedOrEnc = data
	if !r.encrypted && dt == 0x30 {
		if err := m.fillPDU(data); err != nil {
			return nil, nil, err
		}
	}
	return m, r, nil
}

func (m *v3msg) fillPDU(scoped []byte) error {
	_, _, rest, err := readTLV(scoped) // contextEngineID
	if err != nil {
		return err
	}
	_, _, rest, err = readTLV(rest) // contextName
	if err != nil {
		return err
	}
	tag, pdu, _, err := readTLV(rest)
	if err != nil {
		return err
	}
	m.pduTag, m.pdu = tag, pdu
	return nil
}

// verifyAndOpen yanıtın HMAC'ini doğrular, gerekiyorsa şifresini çözer.
func (s *snmpSession) verifyAndOpen(raw []byte) (*v3msg, error) {
	m, r, err := parseV3Raw(raw)
	if err != nil {
		return nil, err
	}
	authed := m.flags&0x01 != 0
	if s.level >= 1 && authed {
		if r.authLen != s.macLen {
			return nil, errors.New("SNMPv3 yanıt imzası geçersiz uzunlukta")
		}
		cp := append([]byte{}, raw...)
		for i := 0; i < r.authLen; i++ {
			cp[r.authOff+i] = 0
		}
		mac := hmac.New(s.newHash, s.authKey)
		mac.Write(cp)
		if !hmac.Equal(mac.Sum(nil)[:s.macLen], r.authParams) {
			return nil, errors.New("SNMPv3 yanıt imzası doğrulanamadı (kimlik doğrulama parolası yanlış olabilir)")
		}
	}
	if r.encrypted {
		if m.flags&0x02 == 0 || s.level != 3 {
			return nil, errors.New("beklenmeyen şifreli SNMPv3 yanıtı")
		}
		plain, err := s.decrypt(r.scopedOrEnc, r.privPrm, m.boots, m.etime)
		if err != nil {
			return nil, err
		}
		tag, sc, _, err := readTLV(plain)
		if err != nil || tag != 0x30 {
			return nil, errors.New("SNMPv3 şifre çözme başarısız (gizlilik parolası/protokolü yanlış olabilir)")
		}
		if err := m.fillPDU(sc); err != nil {
			return nil, errors.New("SNMPv3 şifre çözme başarısız (gizlilik parolası/protokolü yanlış olabilir)")
		}
	}
	if m.pduTag != 0xA8 && s.level >= 1 && !authed {
		return nil, errors.New("SNMPv3 yanıtı imzasız; reddedildi")
	}
	return m, nil
}

func (s *snmpSession) decrypt(enc, privParams []byte, boots, etime int64) ([]byte, error) {
	if len(privParams) != 8 {
		return nil, errors.New("geçersiz SNMPv3 gizlilik parametresi")
	}
	if strings.ToUpper(s.a.PrivProto) == "DES" {
		if len(enc)%8 != 0 || len(enc) == 0 {
			return nil, errors.New("geçersiz DES verisi")
		}
		iv := make([]byte, 8)
		for i := range iv {
			iv[i] = s.privKey[8+i] ^ privParams[i]
		}
		blk, _ := des.NewCipher(s.privKey[:8])
		out := make([]byte, len(enc))
		cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, enc)
		return out, nil
	}
	blk, _ := aes.NewCipher(s.privKey)
	out := make([]byte, len(enc))
	cipher.NewCFBDecrypter(blk, aesIV(boots, etime, privParams)).XORKeyStream(out, enc)
	return out, nil
}

var reportErrs = map[string]string{
	"1.3.6.1.6.3.15.1.1.1.0": "SNMPv3 güvenlik düzeyi desteklenmiyor",
	"1.3.6.1.6.3.15.1.1.2.0": "zaman penceresi dışında",
	"1.3.6.1.6.3.15.1.1.3.0": "SNMPv3 kullanıcı adı bilinmiyor",
	"1.3.6.1.6.3.15.1.1.4.0": "SNMPv3 motor kimliği bilinmiyor",
	"1.3.6.1.6.3.15.1.1.5.0": "SNMPv3 kimlik doğrulama parolası/protokolü yanlış",
	"1.3.6.1.6.3.15.1.1.6.0": "SNMPv3 gizlilik parolası/protokolü yanlış (şifre çözülemedi)",
}

// do tek bir GET (0xA0) veya GETNEXT (0xA1) isteği gönderir.
func (s *snmpSession) do(tag byte, oid string) (snmpVal, error) {
	reqID := s.id()
	pdu, err := buildPDU(tag, reqID, oid)
	if err != nil {
		return snmpVal{}, err
	}
	if s.a.Version != "3" {
		ver := int64(1)
		if s.a.Version == "1" {
			ver = 0
		}
		c := s.a.Community
		if c == "" {
			c = "public"
		}
		resp, err := s.exchange(tlv(0x30, bytes.Join([][]byte{berInt(ver), tlv(0x04, []byte(c)), pdu}, nil)))
		if err != nil {
			return snmpVal{}, err
		}
		t, body, _, err := readTLV(resp)
		if err != nil || t != 0x30 {
			return snmpVal{}, errors.New("geçersiz SNMP yanıtı")
		}
		_, _, body, err = readTLV(body)
		if err != nil {
			return snmpVal{}, err
		}
		_, _, body, err = readTLV(body)
		if err != nil {
			return snmpVal{}, err
		}
		t, pb, _, err := readTLV(body)
		if err != nil || t != 0xA2 {
			return snmpVal{}, errors.New("SNMP GetResponse alınamadı")
		}
		return decodePDU(pb, reqID, s.a.Version == "1")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if s.engineID == nil {
			if err := s.discover(); err != nil {
				return snmpVal{}, err
			}
		}
		pdu, _ = buildPDU(tag, reqID, oid)
		resp, err := s.exchange(s.buildV3(pdu, false))
		if err != nil {
			return snmpVal{}, err
		}
		m, err := s.verifyAndOpen(resp)
		if err != nil {
			return snmpVal{}, err
		}
		if m.pduTag == 0xA8 { // Report
			v, derr := decodePDU(m.pdu, -1, false)
			if v.OID == "1.3.6.1.6.3.15.1.1.2.0" && attempt == 0 {
				s.boots, s.etime, s.synced = m.boots, m.etime, time.Now()
				continue
			}
			if msg, ok := reportErrs[v.OID]; ok {
				return snmpVal{}, errors.New(msg)
			}
			if derr != nil {
				return snmpVal{}, derr
			}
			return snmpVal{}, errors.New("SNMPv3 rapor hatası: " + v.OID)
		}
		if m.pduTag != 0xA2 {
			return snmpVal{}, errors.New("SNMP GetResponse alınamadı")
		}
		return decodePDU(m.pdu, reqID, false)
	}
	return snmpVal{}, errors.New("SNMPv3 zaman eşitlemesi başarısız")
}

// decodePDU yanıt PDU'sundan (etiket hariç) ilk varbind'i çıkarır. reqID<0 ise kimlik denetlenmez.
func decodePDU(pdu []byte, reqID int64, v1 bool) (snmpVal, error) {
	_, idv, pdu, err := readTLV(pdu)
	if err != nil {
		return snmpVal{}, err
	}
	if reqID >= 0 && parseInt(idv) != reqID {
		return snmpVal{}, errors.New("SNMP istek kimliği uyuşmuyor")
	}
	_, es, pdu, err := readTLV(pdu)
	if err != nil {
		return snmpVal{}, err
	}
	_, _, pdu, err = readTLV(pdu)
	if err != nil {
		return snmpVal{}, err
	}
	if st := parseInt(es); st != 0 && reqID >= 0 {
		if st == 2 && v1 {
			return snmpVal{}, errSNMPEnd
		}
		names := map[int64]string{1: "tooBig", 2: "noSuchName", 3: "badValue", 4: "readOnly", 5: "genErr", 6: "noAccess", 16: "authorizationError"}
		n := names[st]
		if n == "" {
			n = strconv.FormatInt(st, 10)
		}
		return snmpVal{}, fmt.Errorf("SNMP hata durumu: %s", n)
	}
	_, vbl, _, err := readTLV(pdu)
	if err != nil {
		return snmpVal{}, err
	}
	_, vb, _, err := readTLV(vbl)
	if err != nil {
		return snmpVal{}, errors.New("SNMP yanıtında değer yok")
	}
	_, ob, vb, err := readTLV(vb)
	if err != nil {
		return snmpVal{}, err
	}
	vt, v, _, err := readTLV(vb)
	if err != nil {
		return snmpVal{}, err
	}
	out := snmpVal{OID: oidString(ob), Type: vt, Raw: v}
	if vt == 0x82 {
		return out, errSNMPEnd
	}
	if vt == 0x80 || vt == 0x81 {
		return out, errors.New("OID bulunamadı (noSuchObject/noSuchInstance)")
	}
	return out, nil
}

func (s *snmpSession) Get(oid string) (snmpVal, error) { return s.do(0xA0, oid) }

// Walk base altındaki tüm değerleri GETNEXT ile gezer (en çok max kayıt).
func (s *snmpSession) Walk(ctx context.Context, base string, max int) ([]snmpVal, error) {
	var out []snmpVal
	cur := base
	for len(out) < max {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		v, err := s.do(0xA1, cur)
		if err == errSNMPEnd {
			break
		}
		if err != nil {
			if len(out) > 0 && strings.Contains(err.Error(), "noSuch") {
				break
			}
			return out, err
		}
		if !strings.HasPrefix(v.OID, base+".") || v.OID == cur {
			break
		}
		out = append(out, v)
		cur = v.OID
	}
	return out, nil
}

// dialSNMP hedefe UDP bağlantısı açar ve oturum başlatır.
func dialSNMP(ctx context.Context, d *net.Dialer, target string, block bool, a SNMPAuth) (*snmpSession, func(), error) {
	host, port, err := splitTarget(target, 161)
	if err != nil {
		return nil, nil, err
	}
	ip, err := resolveIP(ctx, host, block)
	if err != nil {
		return nil, nil, err
	}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return nil, nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	s, err := newSession(conn, a)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return s, func() { conn.Close() }, nil
}
