// Package probe agent gerektirmeyen (agentless) sağlık kontrollerini çalıştırır: HTTP, TCP ve PostgreSQL.
package probe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
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
	DSCP      int    `json:"dscp,omitempty"`         // trace: paketlere yazılacak DSCP işareti (0-63; 0 = işaretsiz)

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

	// host türü, method=ssh: kullanıcı, kasadaki SSH anahtarının kimliği ve sabitlenmiş sunucu anahtarı
	SSHUser  string `json:"ssh_user,omitempty"`
	SecretID int64  `json:"secret_id,omitempty"`
	HostKey  string `json:"host_key,omitempty"` // "ssh-ed25519 AAAA..." (ilk bağlantıda öğrenilir, sonra sabitlenir)
	SSHKey   []byte `json:"-"`                  // çalışma anında kasadan çözülür; saklanmaz
	SSHPass  []byte `json:"-"`                  // anahtar yerine parola kullanılacaksa (kasa kaydı türü "password")
	// SSH ile toplanacak veriler (cpu, mem, swap, disk, uptime, load, conns, listen, addr); boş = eski davranış
	// (cpu/mem/disk Metrics'e göre, uptime/conns/listen/addr her zaman). Mounts: kaydedilecek dizinler (boş = hepsi).
	Collect   []string `json:"collect,omitempty"`
	Mounts    []string `json:"mounts,omitempty"`
	SSHLegacy bool     `json:"ssh_legacy,omitempty"` // eski sshd uyumluluğu: zayıf anahtar değişimi/şifre/imza algoritmalarına izin verir (varsayılan kapalı)

	// snmpx türü: hazır şablon, arayüz filtresi (düzenli ifade) ve özel OID listesi
	Template string      `json:"template,omitempty"`
	IfFilter string      `json:"if_filter,omitempty"`
	OIDs     []CustomOID `json:"oids,omitempty"`
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
var Types = []string{"http", "tcp", "postgres", "icmp", "snmp", "dns", "tls", "udp", "ntp", "host", "snmpx", "trace", "pgstats"}

type Spec struct {
	Type    string // Types içindeki değerlerden biri
	Target  string
	Timeout time.Duration
	HTTP    HTTPOptions
	Opts    Options

	// OnHostKey ssh probe'u sunucu anahtarını ilk kez öğrendiğinde çağrılır (sabitlemek için).
	OnHostKey func(line string)

	// Çıkış işaretçileri: doluysa ilgili probe türü ek tablo verisini buraya yazar (host/ssh: bağlantılar,
	// pgstats: yavaş sorgular). Boşsa o veri toplanmaz.
	ConnOut *ConnSnapshot
	StmtOut *StmtSnapshot
	SurfOut *HTTPSurface // http: TLS sürümü, güvenlik başlıkları ve banner
	Det     *Details     // RunX tarafından doldurulur: türe özgü ölçüm ayrıntıları
}

// Detail bir probe ölçümünün insan tarafından okunan tek bir ayrıntısıdır (ör. "Sertifika sahibi" → "example.com").
type Detail struct {
	K string `json:"k"`
	V string `json:"v"`
}

// Details probe türlerinin ayrıntı topladığı güvenli (nil olabilen) listedir. Gizli değer (parola, topluluk adı,
// bağlantı dizesi) eklenmez.
type Details struct {
	mu sync.Mutex
	L  []Detail
}

// Add boş olmayan bir ayrıntı ekler (en çok 60 satır, değer en çok 400 karakter).
func (d *Details) Add(k string, v any) {
	if d == nil {
		return
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "<nil>" {
		return
	}
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.L) < 60 {
		d.L = append(d.L, Detail{K: k, V: s})
	}
}

// Result bir probe sonucudur; Extra türe özgü ek metrikleri taşır (ör. packet_loss_pct, cert_days_left).
type Result struct {
	Up      bool
	Latency time.Duration
	Err     error
	Extra   map[string]float64
	Timing  map[string]float64 // yalnızca http: dns, connect, tls, ttfb, transfer, total (ms)
	Detail  []Detail           // son ölçümün ayrıntıları (probes.last_detail)
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
	det := &Details{}
	spec.Det = det

	var err error
	var extra map[string]float64
	var timing map[string]float64
	switch spec.Type {
	case "http":
		o := spec.HTTP
		method := o.Method
		if method == "" {
			method = "GET"
		}
		client := NewClient(spec.Timeout, blockPrivate, o.SkipTLSVerify, !o.NoFollowRedirects, nil)
		tm := &HTTPTiming{}
		var resp HTTPResponse
		resp, err = DoHTTP(ctx, client, HTTPRequest{Method: method, URL: spec.Target, Headers: o.Headers, Body: o.Body,
			ExpectStatus: o.ExpectStatus, ExpectText: o.ExpectText, Trace: tm})
		if resp.Status != 0 {
			det.Add("HTTP durum kodu", fmt.Sprintf("%d %s", resp.Status, http.StatusText(resp.Status)))
			det.Add("Protokol", resp.Proto)
			det.Add("TLS sürümü", resp.TLS)
			det.Add("Sunucu başlığı", resp.Header.Get("Server"))
			det.Add("İçerik türü", resp.Header.Get("Content-Type"))
			det.Add("İçerik uzunluğu", resp.Header.Get("Content-Length"))
			det.Add("Yönlendirme adresi", resp.Header.Get("Location"))
			det.Add("Güvenlik başlıkları", strings.Join(SurfaceOf(resp).Headers, ", "))
		}
		timing = tm.Millis(time.Since(start))
		if timing != nil {
			extra = map[string]float64{}
			for k, v := range timing {
				if k != "total" {
					extra["http_"+k+"_ms"] = v
				}
			}
		}
	case "tcp":
		err = tcpProbe(ctx, dialer, spec)
	case "icmp":
		var rtt time.Duration
		rtt, extra, err = icmpProbe(ctx, spec, blockPrivate)
		if err == nil {
			return Result{Up: true, Latency: rtt, Extra: extra, Detail: det.L}
		}
	case "snmp":
		extra, err = snmpProbe(ctx, dialer, spec, blockPrivate)
	case "snmpx":
		det.Add("Şablon", spec.Opts.Template)
		extra, err = snmpxProbe(ctx, dialer, spec, blockPrivate)
		det.Add("Ölçülen değer sayısı", len(extra))
	case "trace":
		extra, err = pathProbe(ctx, spec, blockPrivate)
	case "host":
		if spec.Opts.Method == "ssh" {
			det.Add("Yöntem", "SSH")
			det.Add("SSH kullanıcısı", spec.Opts.SSHUser)
			det.Add("Eski SSH uyumluluğu", map[bool]string{true: "açık"}[spec.Opts.SSHLegacy])
			det.Add("Kaydedilecek dizinler", strings.Join(spec.Opts.Mounts, ", "))
		} else {
			det.Add("Yöntem", "SNMP")
			det.Add("SNMP sürümü", spec.Opts.Version)
		}
		extra, err = hostProbe(ctx, dialer, spec, blockPrivate)
		det.Add("Ölçülen değer sayısı", len(extra))
	case "dns":
		extra, err = dnsProbe(ctx, dialer, spec)
	case "tls":
		extra, err = tlsProbe(ctx, dialer, spec)
	case "udp":
		err = udpProbe(ctx, dialer, spec, blockPrivate)
	case "ntp":
		extra, err = ntpProbe(ctx, dialer, spec, blockPrivate)
	case "postgres", "pgstats":
		var c *pq.Connector
		c, err = pq.NewConnector(spec.Target)
		if err == nil {
			c.Dialer(pqDialer{dialer})
			db := sql.OpenDB(c)
			db.SetMaxOpenConns(1)
			var one int
			err = db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
			if err == nil {
				var ver, dbn string
				if db.QueryRowContext(ctx, `SELECT split_part(version(), ' ', 2), current_database()`).Scan(&ver, &dbn) == nil {
					det.Add("PostgreSQL sürümü", ver)
					det.Add("Veritabanı", dbn)
				}
			}
			if err == nil && spec.Type == "pgstats" {
				extra, err = pgStats(ctx, db)
				if err == nil && spec.StmtOut != nil {
					*spec.StmtOut = *collectStatements(ctx, db, extra)
				}
			}
			db.Close()
		}
	default:
		err = fmt.Errorf("bilinmeyen probe türü: %s", spec.Type)
	}
	return Result{Up: err == nil, Latency: time.Since(start), Err: tlsHint(err), Extra: extra, Timing: timing, Detail: det.L}
}

// Guard dışarıdan (bildirim gibi) dial denetimi için guard'ı açar.
func Guard(block bool) func(network, address string, c syscall.RawConn) error { return guard(block) }
