// Package probe agent gerektirmeyen (agentless) sağlık kontrollerini çalıştırır: HTTP, TCP ve PostgreSQL.
package probe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/lib/pq"
)

// guard SaaS modunda probe'ların sunucunun iç ağına (loopback, özel ağ, link-local) erişmesini engeller.
// Kontrol, DNS çözümlemesinden sonra gerçek bağlantı adresi üzerinde yapıldığı için DNS rebinding'e de dayanıklıdır.
func guard(block bool) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if !block {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return errors.New("geçersiz hedef adres")
		}
		if blockedIP(ip) {
			return errors.New("SaaS modunda özel veya yerel ağ adreslerine probe yapılamaz")
		}
		return nil
	}
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast()
}

type pqDialer struct{ d *net.Dialer }

func (p pqDialer) Dial(network, address string) (net.Conn, error) { return p.d.Dial(network, address) }
func (p pqDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	d := *p.d
	d.Timeout = timeout
	return d.Dial(network, address)
}

// HTTPOptions yalnızca "http" probe'u için geçerlidir. Sıfır değerler güvenli varsayılanlardır
// (GET, 200-399, TLS doğrulaması açık, yönlendirmeler izlenir).
type HTTPOptions struct {
	Method            string
	Headers           map[string]string
	Body              string
	ExpectStatus      string
	ExpectText        string
	SkipTLSVerify     bool
	NoFollowRedirects bool
}

// Options http dışındaki türlere özgü seçeneklerdir (kullanılmayanlar yok sayılır).
type Options struct {
	Count     int    `json:"count,omitempty"`        // icmp: gönderilecek paket sayısı (varsayılan 3)
	Community string `json:"community,omitempty"`    // snmp topluluk adı (varsayılan "public"); gizli değerdir
	OID       string `json:"oid,omitempty"`          // snmp: sorgulanacak OID (varsayılan sysUpTime)
	Version   string `json:"snmp_version,omitempty"` // snmp: "2c" (varsayılan) veya "1"
	Server    string `json:"server,omitempty"`       // dns: özel DNS sunucusu (host veya host:port)
	Record    string `json:"record,omitempty"`       // dns: A, AAAA, MX, TXT, CNAME, NS (varsayılan A)
	Send      string `json:"send,omitempty"`         // tcp/udp: bağlanınca gönderilecek metin (\r \n kullanılabilir)
	Expect    string `json:"expect,omitempty"`       // tcp/udp/snmp/dns: yanıtta aranacak metin

	// SNMPv3 (USM) kimlik bilgileri; parolalar gizli değerdir.
	User      string `json:"user,omitempty"`
	AuthProto string `json:"auth_proto,omitempty"` // MD5, SHA, SHA224, SHA256, SHA384, SHA512 (boş = kimlik doğrulama yok)
	AuthPass  string `json:"auth_pass,omitempty"`
	PrivProto string `json:"priv_proto,omitempty"` // DES veya AES (boş = gizlilik yok)
	PrivPass  string `json:"priv_pass,omitempty"`
	Context   string `json:"context,omitempty"`

	// host türü: toplanacak ölçümler ("cpu", "mem", "disk"; boş = hepsi)
	Method  string   `json:"method,omitempty"` // "snmp" (varsayılan) veya "ssh"
	Metrics []string `json:"metrics,omitempty"`
}

// SNMPAuth seçeneklerden SNMP kimlik bilgilerini çıkarır.
func (o Options) SNMPAuth() SNMPAuth {
	v := o.Version
	if v == "" {
		v = "2c"
	}
	c := o.Community
	if c == "" {
		c = "public"
	}
	return SNMPAuth{Version: v, Community: c, User: o.User, AuthProto: o.AuthProto, AuthPass: o.AuthPass,
		PrivProto: o.PrivProto, PrivPass: o.PrivPass, Context: o.Context}
}

// HasSecrets hangi gizli alanların dolu olduğunu söyler; Redacted gizli alanları boşaltır.
func (o Options) Redacted() Options {
	o.Community, o.AuthPass, o.PrivPass = "", "", ""
	return o
}

// Types desteklenen probe türleridir.
var Types = []string{"http", "tcp", "postgres", "icmp", "snmp", "dns", "tls", "udp", "ntp", "host"}

type Spec struct {
	Type    string // Types içindeki değerlerden biri
	Target  string
	Timeout time.Duration
	HTTP    HTTPOptions
	Opts    Options
}

// Result bir probe sonucudur; Extra türe özgü ek metrikleri taşır (ör. packet_loss_pct, cert_days_left).
type Result struct {
	Up      bool
	Latency time.Duration
	Err     error
	Extra   map[string]float64
}

// Run tek bir probe çalıştırır ve ulaşılabilirlik ile gecikmeyi döndürür.
func Run(ctx context.Context, spec Spec, blockPrivate bool) (bool, time.Duration, error) {
	r := RunX(ctx, spec, blockPrivate)
	return r.Up, r.Latency, r.Err
}

// RunX Run gibidir ama ek metrikleri de döndürür.
func RunX(ctx context.Context, spec Spec, blockPrivate bool) Result {
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	dialer := &net.Dialer{Timeout: spec.Timeout, Control: guard(blockPrivate)}
	start := time.Now()

	var err error
	var extra map[string]float64
	switch spec.Type {
	case "http":
		o := spec.HTTP
		method := o.Method
		if method == "" {
			method = "GET"
		}
		client := NewClient(spec.Timeout, blockPrivate, o.SkipTLSVerify, !o.NoFollowRedirects, nil)
		_, err = DoHTTP(ctx, client, HTTPRequest{Method: method, URL: spec.Target, Headers: o.Headers, Body: o.Body,
			ExpectStatus: o.ExpectStatus, ExpectText: o.ExpectText})
	case "tcp":
		err = tcpProbe(ctx, dialer, spec)
	case "icmp":
		var rtt time.Duration
		rtt, extra, err = icmpProbe(ctx, spec, blockPrivate)
		if err == nil {
			return Result{Up: true, Latency: rtt, Extra: extra}
		}
	case "snmp":
		extra, err = snmpProbe(ctx, dialer, spec, blockPrivate)
	case "host":
		extra, err = hostProbe(ctx, dialer, spec, blockPrivate)
	case "dns":
		extra, err = dnsProbe(ctx, dialer, spec)
	case "tls":
		extra, err = tlsProbe(ctx, dialer, spec)
	case "udp":
		err = udpProbe(ctx, dialer, spec, blockPrivate)
	case "ntp":
		extra, err = ntpProbe(ctx, dialer, spec, blockPrivate)
	case "postgres":
		var c *pq.Connector
		c, err = pq.NewConnector(spec.Target)
		if err == nil {
			c.Dialer(pqDialer{dialer})
			db := sql.OpenDB(c)
			db.SetMaxOpenConns(1)
			var one int
			err = db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
			db.Close()
		}
	default:
		err = fmt.Errorf("bilinmeyen probe türü: %s", spec.Type)
	}
	return Result{Up: err == nil, Latency: time.Since(start), Err: err, Extra: extra}
}
