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
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("SaaS modunda özel veya yerel ağ adreslerine probe yapılamaz")
		}
		return nil
	}
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

type Spec struct {
	Type    string // http, tcp, postgres
	Target  string
	Timeout time.Duration
	HTTP    HTTPOptions
}

// Run tek bir probe çalıştırır ve ulaşılabilirlik ile gecikmeyi döndürür.
func Run(ctx context.Context, spec Spec, blockPrivate bool) (bool, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	dialer := &net.Dialer{Timeout: spec.Timeout, Control: guard(blockPrivate)}
	start := time.Now()

	var err error
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
		var conn net.Conn
		conn, err = dialer.DialContext(ctx, "tcp", spec.Target)
		if err == nil {
			conn.Close()
		}
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
	return err == nil, time.Since(start), err
}
