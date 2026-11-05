// Package siem olayları (denetim kayıtları, alarmlar) bir SIEM'e syslog (UDP/TCP/TLS) veya HTTP ile iletir.
// Biçimler: RFC 5424 syslog, CEF, JSON satırı.
package siem

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	Protocols = []string{"udp", "tcp", "tls", "http"}
	Formats   = []string{"rfc5424", "cef", "json"}
)

// Config bir SIEM hedefinin ayarlarıdır. Gizli alan: Token.
type Config struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	URL      string `json:"url,omitempty"`
	Auth     string `json:"auth,omitempty"` // http: none, splunk, bearer
	Token    string `json:"token,omitempty"`
	Format   string `json:"format"`
	Facility int    `json:"facility"` // 0-23 (varsayılan 16 = local0)
	AppName  string `json:"app_name,omitempty"`
	SkipTLS  bool   `json:"skip_tls_verify,omitempty"`
	Audit    bool   `json:"audit"`
	Alerts   bool   `json:"alerts"`
	MinSev   string `json:"min_severity,omitempty"` // alarmlar için en düşük önem
}

func (c Config) Redacted() (Config, map[string]bool) {
	has := map[string]bool{"token": c.Token != ""}
	c.Token = ""
	return c, has
}

func (c *Config) Keep(old Config) {
	if c.Token == "" {
		c.Token = old.Token
	}
}

// Event iletilecek olaydır.
type Event struct {
	Time     time.Time
	Type     string // audit, alert, test
	Severity string // info, warning, critical
	Actor    string
	Action   string
	Detail   string
	Asset    string
	Rule     string
	Value    string
	Link     string
}

type Env struct {
	Guard  func(network, address string, c syscall.RawConn) error
	Client *http.Client
}

func in(v string, l []string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// Validate ayarları denetler ve eksik varsayılanları doldurur.
func Validate(c *Config) error {
	if !in(c.Protocol, Protocols) {
		return errors.New("protokol " + strings.Join(Protocols, ", ") + " olmalı")
	}
	if c.Format == "" {
		c.Format = "rfc5424"
	}
	if !in(c.Format, Formats) {
		return errors.New("biçim " + strings.Join(Formats, ", ") + " olmalı")
	}
	if c.Facility == 0 && c.AppName == "" {
		c.Facility = 16
	}
	if c.Facility < 0 || c.Facility > 23 {
		return errors.New("facility 0-23 olmalı")
	}
	if !c.Audit && !c.Alerts {
		return errors.New("en az bir kaynak (denetim kayıtları veya alarmlar) seçin")
	}
	if c.MinSev == "" {
		c.MinSev = "info"
	}
	if !in(c.MinSev, []string{"info", "warning", "critical"}) {
		return errors.New("en düşük önem info, warning veya critical olmalı")
	}
	if len(c.AppName) > 48 || strings.ContainsAny(c.AppName, " \t\r\n") {
		return errors.New("uygulama adı boşluksuz ve en çok 48 karakter olmalı")
	}
	if c.Protocol == "http" {
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("geçerli bir http(s) adresi (url) zorunlu")
		}
		if c.Auth == "" {
			c.Auth = "none"
		}
		if !in(c.Auth, []string{"none", "splunk", "bearer"}) {
			return errors.New("kimlik doğrulama none, splunk veya bearer olmalı")
		}
		if c.Auth != "none" && c.Token == "" {
			return errors.New("token zorunlu")
		}
		return nil
	}
	if strings.TrimSpace(c.Host) == "" || strings.ContainsAny(c.Host, " /") {
		return errors.New("sunucu adresi zorunlu")
	}
	if c.Port == 0 {
		c.Port = map[string]int{"udp": 514, "tcp": 514, "tls": 6514}[c.Protocol]
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port 1-65535 olmalı")
	}
	return nil
}

// Wanted olayın bu hedefe gönderilip gönderilmeyeceğini söyler.
func (c Config) Wanted(e Event) bool {
	switch e.Type {
	case "audit":
		return c.Audit
	case "alert":
		rank := map[string]int{"info": 1, "warning": 2, "critical": 3}
		return c.Alerts && rank[e.Severity] >= rank[c.MinSev]
	}
	return true
}

func syslogSev(s string) int {
	switch s {
	case "critical":
		return 2
	case "warning":
		return 4
	}
	return 6
}

func cefSev(s string) int {
	switch s {
	case "critical":
		return 9
	case "warning":
		return 6
	}
	return 3
}

func (c Config) app() string {
	if c.AppName == "" {
		return "appmon"
	}
	return c.AppName
}

func sdEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, `]`, `\]`, "\n", " ", "\r", " ").Replace(s)
}

func cefHdr(s string) string {
	return strings.NewReplacer(`\`, `\\`, `|`, `\|`, "\n", " ", "\r", " ").Replace(s)
}

func cefExt(s string) string {
	return strings.NewReplacer(`\`, `\\`, `=`, `\=`, "\n", `\n`, "\r", " ").Replace(s)
}

func (e Event) summary() string {
	switch e.Type {
	case "audit":
		return strings.TrimSpace(e.Actor + " " + e.Action + " " + e.Detail)
	case "alert":
		return fmt.Sprintf("%s — %s (%s)", e.Rule, e.Asset, e.Action)
	}
	return "APP_MON deneme olayı"
}

func (e Event) fields() [][2]string {
	f := [][2]string{{"type", e.Type}, {"severity", e.Severity}}
	add := func(k, v string) {
		if v != "" {
			f = append(f, [2]string{k, v})
		}
	}
	add("actor", e.Actor)
	add("action", e.Action)
	add("detail", e.Detail)
	add("asset", e.Asset)
	add("rule", e.Rule)
	add("value", e.Value)
	add("link", e.Link)
	return f
}

// JSON olayı tek satır JSON olarak üretir.
func (c Config) jsonLine(e Event) []byte {
	m := map[string]any{"time": e.Time.UTC().Format(time.RFC3339Nano), "source": c.app()}
	for _, kv := range e.fields() {
		m[kv[0]] = kv[1]
	}
	m["message"] = e.summary()
	b, _ := json.Marshal(m)
	return b
}

// Format olayı seçili biçimde (syslog sarmalayıcısıyla, protokol syslog ise) üretir.
func (c Config) Render(e Event) []byte {
	if c.Protocol == "http" {
		return c.jsonLine(e)
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "-"
	}
	pri := c.Facility*8 + syslogSev(e.Severity)
	if c.Format == "json" {
		return []byte(fmt.Sprintf("<%d>1 %s %s %s - %s - %s", pri, e.Time.UTC().Format(time.RFC3339Nano), host, c.app(), e.Type, c.jsonLine(e)))
	}
	if c.Format == "cef" {
		sig := e.Type + "." + e.Action
		if e.Type == "alert" {
			sig = "alert." + e.Action
		}
		var ext []string
		for _, kv := range e.fields() {
			switch kv[0] {
			case "type", "severity":
				continue
			}
			key := map[string]string{"actor": "suser", "asset": "dvchost", "detail": "msg", "link": "request", "action": "act", "rule": "cs1", "value": "cs2"}[kv[0]]
			ext = append(ext, key+"="+cefExt(kv[1]))
		}
		if e.Rule != "" {
			ext = append(ext, "cs1Label=rule")
		}
		if e.Value != "" {
			ext = append(ext, "cs2Label=value")
		}
		ext = append(ext, "rt="+strconv.FormatInt(e.Time.UnixMilli(), 10))
		return []byte(fmt.Sprintf("<%d>%s %s CEF:0|APP_MON|APP_MON|1.0|%s|%s|%d|%s", pri, e.Time.UTC().Format(time.RFC3339), host, cefHdr(sig), cefHdr(e.summary()), cefSev(e.Severity), strings.Join(ext, " ")))
	}
	var sd strings.Builder
	sd.WriteString("[appmon@32473")
	for _, kv := range e.fields() {
		sd.WriteString(" " + kv[0] + `="` + sdEsc(kv[1]) + `"`)
	}
	sd.WriteString("]")
	return []byte(fmt.Sprintf("<%d>1 %s %s %s - %s %s %s", pri, e.Time.UTC().Format(time.RFC3339Nano), host, c.app(), e.Type, sd.String(), strings.ReplaceAll(e.summary(), "\n", " ")))
}

// Send olayı hedefe iletir.
func Send(ctx context.Context, c Config, e Event, env Env) error {
	if err := Validate(&c); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	msg := c.Render(e)
	if c.Protocol == "http" {
		return sendHTTP(ctx, c, e, msg, env)
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	if env.Guard != nil {
		d.Control = env.Guard
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	switch c.Protocol {
	case "udp":
		conn, err := d.DialContext(ctx, "udp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		if len(msg) > 8000 {
			msg = msg[:8000]
		}
		_, err = conn.Write(msg)
		return err
	case "tcp":
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		_, err = conn.Write(append(msg, '\n'))
		return err
	default: // tls
		raw, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		conn := tls.Client(raw, &tls.Config{ServerName: c.Host, InsecureSkipVerify: c.SkipTLS, MinVersion: tls.VersionTLS12})
		defer conn.Close()
		if err := conn.HandshakeContext(ctx); err != nil {
			return err
		}
		_, err = conn.Write(append(msg, '\n'))
		return err
	}
}

func sendHTTP(ctx context.Context, c Config, e Event, line []byte, env Env) error {
	body := line
	if c.Auth == "splunk" {
		var ev any
		json.Unmarshal(line, &ev)
		body, _ = json.Marshal(map[string]any{"time": float64(e.Time.UnixMilli()) / 1000, "source": c.app(), "sourcetype": "_json", "event": ev})
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	switch c.Auth {
	case "splunk":
		req.Header.Set("Authorization", "Splunk "+c.Token)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	cl := env.Client
	if cl == nil {
		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: c.SkipTLS, MinVersion: tls.VersionTLS12}}
		if env.Guard != nil {
			tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: env.Guard}).DialContext
		}
		cl = &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), c.URL, "<url>"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("sunucu %d döndürdü: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}
