package probe

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// ValidHost host adı veya IP adresi geçerli mi (port ve şema olmadan).
func ValidHost(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	return net.ParseIP(s) != nil || hostRe.MatchString(s)
}

// splitTarget "host" veya "host:port" hedefini ayırır; port yoksa def kullanılır.
func splitTarget(t string, def int) (string, int, error) {
	t = strings.TrimSpace(t)
	if i := strings.LastIndex(t, ":"); i > 0 && !strings.Contains(t[i+1:], "]") && strings.Count(t, ":") == 1 {
		p, err := strconv.Atoi(t[i+1:])
		if err != nil || p < 1 || p > 65535 {
			return "", 0, errors.New("geçersiz port")
		}
		return t[:i], p, nil
	}
	if def == 0 {
		return "", 0, errors.New("host:port biçiminde olmalı")
	}
	return t, def, nil
}

// resolveIP hedef adını çözer (IPv4 öncelikli); SaaS modunda özel/yerel adresleri reddeder.
func resolveIP(ctx context.Context, host string, block bool) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if block && blockedIP(ip) {
			return nil, errors.New("SaaS modunda özel veya yerel ağ adreslerine probe yapılamaz")
		}
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("adres çözümlenemedi (%s)", host)
	}
	pick := ips[0].IP
	for _, a := range ips {
		if a.IP.To4() != nil {
			pick = a.IP
			break
		}
	}
	if block && blockedIP(pick) {
		return nil, errors.New("SaaS modunda özel veya yerel ağ adreslerine probe yapılamaz")
	}
	return pick, nil
}

func unescape(s string) string {
	return strings.NewReplacer(`\r`, "\r", `\n`, "\n", `\t`, "\t").Replace(s)
}

// ---- TCP (isteğe bağlı gönder/bekle) ----

func tcpProbe(ctx context.Context, d *net.Dialer, spec Spec) error {
	conn, err := d.DialContext(ctx, "tcp", spec.Target)
	if err != nil {
		return err
	}
	defer conn.Close()
	if spec.Opts.Send == "" && spec.Opts.Expect == "" {
		return nil
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if spec.Opts.Send != "" {
		if _, err := io.WriteString(conn, unescape(spec.Opts.Send)); err != nil {
			return err
		}
	}
	if spec.Opts.Expect == "" {
		return nil
	}
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for len(buf) < 4096 {
		n, err := conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if strings.Contains(string(buf), spec.Opts.Expect) {
			return nil
		}
		if err != nil {
			break
		}
	}
	return fmt.Errorf("yanıtta %q bulunamadı", spec.Opts.Expect)
}

// ---- UDP ----

func udpProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) error {
	host, port, err := splitTarget(spec.Target, 0)
	if err != nil {
		return err
	}
	ip, err := resolveIP(ctx, host, block)
	if err != nil {
		return err
	}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	payload := unescape(spec.Opts.Send)
	if payload == "" {
		payload = "\x00"
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return errors.New("yanıt alınamadı (UDP servisi yanıt vermiyor veya port kapalı)")
	}
	if spec.Opts.Expect != "" && !strings.Contains(string(buf[:n]), spec.Opts.Expect) {
		return fmt.Errorf("yanıtta %q bulunamadı", spec.Opts.Expect)
	}
	return nil
}

// ---- NTP (SNTP) ----

const ntpEpoch = 2208988800 // 1900 -> 1970 saniye farkı

func ntpTime(b []byte) time.Time {
	sec := binary.BigEndian.Uint32(b[0:4])
	frac := binary.BigEndian.Uint32(b[4:8])
	return time.Unix(int64(sec)-ntpEpoch, int64(float64(frac)*1e9/float64(1<<32)))
}

func ntpProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	host, port, err := splitTarget(spec.Target, 123)
	if err != nil {
		return nil, err
	}
	ip, err := resolveIP(ctx, host, block)
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
	req := make([]byte, 48)
	req[0] = 0x1B // LI=0, VN=3, Mode=3 (istemci)
	t1 := time.Now()
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	resp := make([]byte, 128)
	n, err := conn.Read(resp)
	t4 := time.Now()
	if err != nil || n < 48 {
		return nil, errors.New("NTP yanıtı alınamadı")
	}
	stratum := resp[1]
	if stratum == 0 || stratum > 15 {
		return nil, fmt.Errorf("NTP sunucusu senkronize değil (stratum %d)", stratum)
	}
	t2, t3 := ntpTime(resp[32:40]), ntpTime(resp[40:48])
	offset := (t2.Sub(t1) + t3.Sub(t4)) / 2
	return map[string]float64{"ntp_offset_ms": math.Abs(float64(offset.Microseconds()) / 1000)}, nil
}

// ---- DNS ----

func dnsProbe(ctx context.Context, d *net.Dialer, spec Spec) (map[string]float64, error) {
	r := net.DefaultResolver
	if srv := strings.TrimSpace(spec.Opts.Server); srv != "" {
		if !strings.Contains(srv, ":") || strings.Count(srv, ":") > 1 {
			srv = net.JoinHostPort(strings.Trim(srv, "[]"), "53")
		}
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, srv)
		}}
	}
	name := strings.TrimSpace(spec.Target)
	var answers []string
	var err error
	switch strings.ToUpper(spec.Opts.Record) {
	case "", "A":
		var ips []net.IP
		ips, err = r.LookupIP(ctx, "ip4", name)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "AAAA":
		var ips []net.IP
		ips, err = r.LookupIP(ctx, "ip6", name)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "MX":
		var mx []*net.MX
		mx, err = r.LookupMX(ctx, name)
		for _, m := range mx {
			answers = append(answers, m.Host)
		}
	case "TXT":
		answers, err = r.LookupTXT(ctx, name)
	case "CNAME":
		var c string
		c, err = r.LookupCNAME(ctx, name)
		if c != "" {
			answers = []string{c}
		}
	case "NS":
		var ns []*net.NS
		ns, err = r.LookupNS(ctx, name)
		for _, n := range ns {
			answers = append(answers, n.Host)
		}
	default:
		return nil, errors.New("kayıt türü A, AAAA, MX, TXT, CNAME veya NS olmalı")
	}
	if err != nil {
		return nil, err
	}
	if len(answers) == 0 {
		return nil, errors.New("kayıt bulunamadı")
	}
	if e := spec.Opts.Expect; e != "" && !strings.Contains(strings.ToLower(strings.Join(answers, " ")), strings.ToLower(e)) {
		return nil, fmt.Errorf("yanıtta %q yok (dönen: %s)", e, strings.Join(answers, ", "))
	}
	return map[string]float64{"dns_answers": float64(len(answers))}, nil
}

// ---- TLS sertifikası ----

func tlsProbe(ctx context.Context, d *net.Dialer, spec Spec) (map[string]float64, error) {
	host, port, err := splitTarget(spec.Target, 443)
	if err != nil {
		return nil, err
	}
	td := &tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: host, InsecureSkipVerify: spec.HTTP.SkipTLSVerify, MinVersion: tls.VersionTLS12, RootCAs: ExtraRoots}}
	conn, err := td.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("sertifika alınamadı")
	}
	left := time.Until(certs[0].NotAfter).Hours() / 24
	return map[string]float64{"cert_days_left": math.Round(left*10) / 10}, nil
}
