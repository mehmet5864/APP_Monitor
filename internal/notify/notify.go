// Package notify alarm bildirimlerini gönderir: e-posta (SMTP, Microsoft Graph, Exchange EWS, Windows MAPI/Outlook)
// ve webhook (genel, Slack, Teams, Telegram). Yalnızca Go standart kütüphanesi kullanılır.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Kinds desteklenen kanal türleridir.
var Kinds = []string{"smtp", "graph", "ews", "mapi", "webhook", "slack", "teams", "telegram"}

// Config bir kanalın ayarlarıdır; alanların hangisinin kullanıldığı türe bağlıdır. Gizli alanlar: Pass, ClientSecret, Token.
type Config struct {
	// smtp
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Security string `json:"security,omitempty"` // none, starttls, ssl
	User     string `json:"user,omitempty"`
	Pass     string `json:"pass,omitempty"`
	SkipTLS  bool   `json:"skip_tls_verify,omitempty"`
	// ortak e-posta alanları
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"` // virgülle ayrılmış
	// graph
	TenantID     string `json:"tenant_id,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	LoginURL     string `json:"login_url,omitempty"` // varsayılan https://login.microsoftonline.com
	GraphURL     string `json:"graph_url,omitempty"` // varsayılan https://graph.microsoft.com
	// ews / webhook / slack / teams
	URL string `json:"url,omitempty"`
	// telegram
	Token  string `json:"token,omitempty"`
	ChatID string `json:"chat_id,omitempty"`
	// mapi (Outlook): isteğe bağlı hesap/profil adı
	Account string `json:"account,omitempty"`
}

// Secrets gizli alanları boşaltılmış bir kopya döndürür ve hangilerinin dolu olduğunu söyler.
func (c Config) Redacted() (Config, map[string]bool) {
	has := map[string]bool{"pass": c.Pass != "", "client_secret": c.ClientSecret != "", "token": c.Token != ""}
	c.Pass, c.ClientSecret, c.Token = "", "", ""
	return c, has
}

// Keep boş bırakılan gizli alanlar için eski değerleri korur.
func (c *Config) Keep(old Config) {
	if c.Pass == "" {
		c.Pass = old.Pass
	}
	if c.ClientSecret == "" {
		c.ClientSecret = old.ClientSecret
	}
	if c.Token == "" {
		c.Token = old.Token
	}
}

// Message gönderilecek bildirimdir.
type Message struct {
	Event    string // open, resolved, test
	Severity string // critical, warning, info
	Subject  string
	Text     string
	Asset    string
	Rule     string
	Value    string
	When     time.Time
	Link     string
}

// Env gönderim ortamıdır.
type Env struct {
	Block  bool                                                   // özel/yerel ağ adreslerini engelle (SaaS)
	Guard  func(network, address string, c syscall.RawConn) error // Block için dial denetimi
	Client *http.Client                                           // http tabanlı kanallar için (nil ise varsayılan)
}

var sevColor = map[string]string{"critical": "#e01b1b", "warning": "#d99a00", "info": "#0b7a75", "resolved": "#2e7d32", "test": "#0b7a75"}
var sevLabel = map[string]string{"critical": "KRİTİK", "warning": "UYARI", "info": "BİLGİ"}

func (m Message) title() string {
	switch m.Event {
	case "resolved":
		return "DÜZELDİ"
	case "test":
		return "TEST"
	}
	if l, ok := sevLabel[m.Severity]; ok {
		return l
	}
	return strings.ToUpper(m.Severity)
}

func (m Message) color() string {
	if m.Event == "resolved" || m.Event == "test" {
		return sevColor[m.Event]
	}
	if c, ok := sevColor[m.Severity]; ok {
		return c
	}
	return "#555555"
}

// HTML renkli bir e-posta gövdesi üretir.
func (m Message) HTML() string {
	row := func(k, v string) string {
		if v == "" {
			return ""
		}
		return `<tr><td style="padding:6px 12px;color:#666;white-space:nowrap">` + html.EscapeString(k) + `</td><td style="padding:6px 12px"><b>` + html.EscapeString(v) + `</b></td></tr>`
	}
	when := ""
	if !m.When.IsZero() {
		when = m.When.Format("02.01.2006 15:04:05")
	}
	link := ""
	if m.Link != "" {
		link = `<p style="padding:0 12px"><a href="` + html.EscapeString(m.Link) + `" style="color:#0b7a75">APP_MON'da aç</a></p>`
	}
	return `<!doctype html><html><body style="margin:0;background:#f3f6f6;font-family:Segoe UI,Arial,sans-serif">` +
		`<div style="max-width:560px;margin:20px auto;background:#fff;border:1px solid #dde3e3;border-radius:8px;overflow:hidden">` +
		`<div style="background:` + m.color() + `;color:#fff;padding:14px 18px;font-size:13px;letter-spacing:.08em">` + html.EscapeString(m.title()) + `</div>` +
		`<div style="padding:16px 18px 8px;font-size:18px"><b>` + html.EscapeString(m.Subject) + `</b></div>` +
		`<table style="border-collapse:collapse;font-size:14px;width:100%">` + row("Hizmet", m.Asset) + row("Kural", m.Rule) + row("Değer", m.Value) + row("Zaman", when) + `</table>` +
		`<p style="padding:8px 18px;color:#444;font-size:14px">` + html.EscapeString(m.Text) + `</p>` + link +
		`<div style="padding:10px 18px;background:#f3f6f6;color:#888;font-size:12px">APP_MON izleme bildirimi</div></div></body></html>`
}

// PlainText düz metin gövde üretir.
func (m Message) PlainText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", m.title(), m.Subject)
	if m.Asset != "" {
		fmt.Fprintf(&b, "Hizmet: %s\n", m.Asset)
	}
	if m.Rule != "" {
		fmt.Fprintf(&b, "Kural: %s\n", m.Rule)
	}
	if m.Value != "" {
		fmt.Fprintf(&b, "Değer: %s\n", m.Value)
	}
	if !m.When.IsZero() {
		fmt.Fprintf(&b, "Zaman: %s\n", m.When.Format("02.01.2006 15:04:05"))
	}
	if m.Text != "" {
		fmt.Fprintf(&b, "\n%s\n", m.Text)
	}
	if m.Link != "" {
		fmt.Fprintf(&b, "\n%s\n", m.Link)
	}
	return b.String()
}

// Validate ayarları türe göre denetler.
func Validate(kind string, c Config) error {
	okKind := false
	for _, k := range Kinds {
		if k == kind {
			okKind = true
		}
	}
	if !okKind {
		return errors.New("tür " + strings.Join(Kinds, ", ") + " olmalı")
	}
	need := func(v, name string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New(name + " zorunlu")
		}
		return nil
	}
	mail := func() error {
		if len(Recipients(c.To)) == 0 {
			return errors.New("alıcı (to) zorunlu")
		}
		for _, r := range Recipients(c.To) {
			if !strings.Contains(r, "@") || strings.ContainsAny(r, "\r\n<>") {
				return errors.New("geçersiz alıcı: " + r)
			}
		}
		return nil
	}
	httpURL := func(u string) error {
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return errors.New("adres http:// veya https:// ile başlayan geçerli bir URL olmalı")
		}
		return nil
	}
	switch kind {
	case "smtp":
		if err := need(c.Host, "sunucu"); err != nil {
			return err
		}
		if err := need(c.From, "gönderen"); err != nil {
			return err
		}
		switch c.Security {
		case "", "none", "starttls", "ssl":
		default:
			return errors.New("güvenlik none, starttls veya ssl olmalı")
		}
		return mail()
	case "graph":
		for _, p := range [][2]string{{c.TenantID, "dizin (tenant) kimliği"}, {c.ClientID, "uygulama (client) kimliği"}, {c.ClientSecret, "istemci sırrı"}, {c.From, "gönderen posta kutusu"}} {
			if err := need(p[0], p[1]); err != nil {
				return err
			}
		}
		return mail()
	case "ews":
		if err := need(c.URL, "EWS adresi"); err != nil {
			return err
		}
		if err := httpURL(c.URL); err != nil {
			return err
		}
		if err := need(c.User, "kullanıcı"); err != nil {
			return err
		}
		return mail()
	case "mapi":
		return mail()
	case "webhook", "slack", "teams":
		if err := need(c.URL, "adres"); err != nil {
			return err
		}
		return httpURL(c.URL)
	case "telegram":
		if err := need(c.Token, "bot anahtarı"); err != nil {
			return err
		}
		return need(c.ChatID, "sohbet kimliği")
	}
	return nil
}

// Recipients virgül/noktalı virgülle ayrılmış alıcıları ayırır.
func Recipients(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Send mesajı kanala gönderir.
func Send(ctx context.Context, kind string, c Config, m Message, env Env) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	if err := Validate(kind, c); err != nil {
		return err
	}
	switch kind {
	case "smtp":
		return sendSMTP(ctx, c, m, env)
	case "graph":
		return sendGraph(ctx, c, m, env)
	case "ews":
		return sendEWS(ctx, c, m, env)
	case "mapi":
		return sendMAPI(ctx, c, m)
	case "webhook":
		return postJSON(ctx, env, c.URL, map[string]any{"event": m.Event, "severity": m.Severity, "subject": m.Subject, "asset": m.Asset,
			"rule": m.Rule, "value": m.Value, "text": m.Text, "time": m.When.Format(time.RFC3339), "link": m.Link}, nil)
	case "slack":
		return postJSON(ctx, env, c.URL, map[string]any{"text": m.PlainText()}, nil)
	case "teams":
		return postJSON(ctx, env, c.URL, map[string]any{"@type": "MessageCard", "@context": "http://schema.org/extensions", "themeColor": strings.TrimPrefix(m.color(), "#"),
			"summary": m.Subject, "title": "[" + m.title() + "] " + m.Subject, "text": strings.ReplaceAll(m.PlainText(), "\n", "<br>")}, nil)
	case "telegram":
		api := "https://api.telegram.org"
		if c.URL != "" {
			api = strings.TrimRight(c.URL, "/")
		}
		return postJSON(ctx, env, api+"/bot"+c.Token+"/sendMessage", map[string]any{"chat_id": c.ChatID, "text": m.PlainText()}, nil)
	}
	return errors.New("bilinmeyen kanal türü")
}

func (e Env) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	d := &net.Dialer{Timeout: 15 * time.Second, Control: e.Guard}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: 15 * time.Second}}
}

func postJSON(ctx context.Context, env Env, u string, body any, hdr map[string]string) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return doReq(env, req)
}

func doReq(env Env, req *http.Request) error {
	resp, err := env.client().Do(req)
	if err != nil {
		return sanitize(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("sunucu %d döndürdü: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// sanitize hata iletisinden adresteki gizli parçaları (Telegram anahtarı gibi) çıkarır.
func sanitize(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s isteği başarısız: %v", ue.Op, ue.Err)
	}
	return err
}

// ---- SMTP ----

type loginAuth struct{ user, pass string }

func (a loginAuth) Start(s *smtp.ServerInfo) (string, []byte, error) {
	for _, m := range s.Auth {
		if m == "PLAIN" {
			return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
		}
	}
	return "LOGIN", nil, nil
}
func (a loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, errors.New("beklenmeyen SMTP sorgusu")
}

func hdrSafe(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func encodeSubject(s string) string {
	s = hdrSafe(s)
	for _, r := range s {
		if r > 126 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

// buildMIME multipart/alternative bir e-posta kurar.
func buildMIME(from string, to []string, m Message) []byte {
	b := "apm" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var w bytes.Buffer
	fmt.Fprintf(&w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n", hdrSafe(from), hdrSafe(strings.Join(to, ", ")), encodeSubject("["+m.title()+"] "+m.Subject), time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&w, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", b)
	part := func(ct, body string) {
		fmt.Fprintf(&w, "--%s\r\nContent-Type: %s; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", b, ct)
		enc := base64.StdEncoding.EncodeToString([]byte(body))
		for len(enc) > 76 {
			w.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		w.WriteString(enc + "\r\n")
	}
	part("text/plain", m.PlainText())
	part("text/html", m.HTML())
	fmt.Fprintf(&w, "--%s--\r\n", b)
	return w.Bytes()
}

func sendSMTP(ctx context.Context, c Config, m Message, env Env) error {
	port := c.Port
	if port == 0 {
		switch c.Security {
		case "ssl":
			port = 465
		case "starttls":
			port = 587
		default:
			port = 25
		}
	}
	d := &net.Dialer{Timeout: 15 * time.Second, Control: env.Guard}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("SMTP sunucusuna bağlanılamadı: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	tcfg := &tls.Config{ServerName: c.Host, InsecureSkipVerify: c.SkipTLS, MinVersion: tls.VersionTLS12}
	if c.Security == "ssl" {
		conn = tls.Client(conn, tcfg)
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP karşılama hatası: %w", err)
	}
	defer cl.Close()
	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("sunucu STARTTLS desteklemiyor")
		}
		if err := cl.StartTLS(tcfg); err != nil {
			return fmt.Errorf("STARTTLS başarısız: %w", err)
		}
	}
	if c.User != "" {
		if err := cl.Auth(loginAuth{c.User, c.Pass}); err != nil {
			return fmt.Errorf("SMTP kimlik doğrulama başarısız: %w", err)
		}
	}
	from := strings.TrimSpace(c.From)
	if err := cl.Mail(addrOnly(from)); err != nil {
		return fmt.Errorf("gönderen reddedildi: %w", err)
	}
	to := Recipients(c.To)
	for _, r := range to {
		if err := cl.Rcpt(addrOnly(r)); err != nil {
			return fmt.Errorf("alıcı reddedildi (%s): %w", r, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMIME(from, to, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("posta kabul edilmedi: %w", err)
	}
	return cl.Quit()
}

func addrOnly(s string) string {
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			return s[i+1 : i+j]
		}
	}
	return s
}

// ---- Microsoft Graph ----

func sendGraph(ctx context.Context, c Config, m Message, env Env) error {
	login := strings.TrimRight(orStr(c.LoginURL, "https://login.microsoftonline.com"), "/")
	graph := strings.TrimRight(orStr(c.GraphURL, "https://graph.microsoft.com"), "/")
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "scope": {"https://graph.microsoft.com/.default"}}
	req, err := http.NewRequestWithContext(ctx, "POST", login+"/"+url.PathEscape(c.TenantID)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := env.client().Do(req)
	if err != nil {
		return sanitize(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tok)
	resp.Body.Close()
	if tok.AccessToken == "" {
		msg := tok.Error
		if msg == "" {
			msg = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
		return errors.New("Microsoft kimlik doğrulaması başarısız: " + firstLine(msg))
	}
	var rcpt []map[string]any
	for _, r := range Recipients(c.To) {
		rcpt = append(rcpt, map[string]any{"emailAddress": map[string]string{"address": addrOnly(r)}})
	}
	body := map[string]any{"message": map[string]any{"subject": "[" + m.title() + "] " + m.Subject,
		"body": map[string]string{"contentType": "HTML", "content": m.HTML()}, "toRecipients": rcpt}, "saveToSentItems": false}
	return postJSON(ctx, env, graph+"/v1.0/users/"+url.PathEscape(addrOnly(c.From))+"/sendMail", body, map[string]string{"Authorization": "Bearer " + tok.AccessToken})
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- Exchange EWS (temel kimlik doğrulama) ----

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func sendEWS(ctx context.Context, c Config, m Message, env Env) error {
	var rcpt strings.Builder
	for _, r := range Recipients(c.To) {
		rcpt.WriteString("<t:Mailbox><t:EmailAddress>" + xmlEsc(addrOnly(r)) + "</t:EmailAddress></t:Mailbox>")
	}
	soap := `<?xml version="1.0" encoding="utf-8"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">` +
		`<soap:Header><t:RequestServerVersion Version="Exchange2013"/></soap:Header><soap:Body>` +
		`<m:CreateItem MessageDisposition="SendOnly"><m:Items><t:Message><t:Subject>` + xmlEsc("["+m.title()+"] "+m.Subject) + `</t:Subject>` +
		`<t:Body BodyType="HTML">` + xmlEsc(m.HTML()) + `</t:Body><t:ToRecipients>` + rcpt.String() + `</t:ToRecipients></t:Message></m:Items></m:CreateItem></soap:Body></soap:Envelope>`
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, strings.NewReader(soap))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.SetBasicAuth(c.User, c.Pass)
	resp, err := env.client().Do(req)
	if err != nil {
		return sanitize(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == 401 {
		return errors.New("EWS kimlik doğrulaması reddedildi (yalnızca temel kimlik doğrulama desteklenir; NTLM/Kerberos için SMTP veya Graph kullanın)")
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("EWS %d döndürdü", resp.StatusCode)
	}
	if !strings.Contains(string(data), `ResponseClass="Success"`) {
		return errors.New("EWS isteği başarılı olmadı: " + firstLine(truncate(string(data), 200)))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- Windows MAPI (Outlook) ----

// sendMAPI Outlook'un MAPI/COM arayüzüyle gönderir. Yalnızca APP_MON Windows'ta doğrudan çalışırken ve Outlook kuruluyken mümkündür.
func sendMAPI(ctx context.Context, c Config, m Message) error {
	if runtime.GOOS != "windows" {
		return errors.New("MAPI (Outlook) yalnızca APP_MON Windows'ta doğrudan çalışırken kullanılabilir; Docker/Linux'ta SMTP, Graph veya EWS seçin")
	}
	script := `$o = New-Object -ComObject Outlook.Application; $mail = $o.CreateItem(0); ` +
		`$mail.To = $env:APM_MAIL_TO; $mail.Subject = $env:APM_MAIL_SUBJECT; $mail.HTMLBody = $env:APM_MAIL_HTML; ` +
		`if ($env:APM_MAIL_ACCOUNT) { foreach ($a in $o.Session.Accounts) { if ($a.SmtpAddress -eq $env:APM_MAIL_ACCOUNT -or $a.DisplayName -eq $env:APM_MAIL_ACCOUNT) { $mail.SendUsingAccount = $a } } }; ` +
		`$mail.Send()`
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "APM_MAIL_TO="+strings.Join(Recipients(c.To), ";"), "APM_MAIL_SUBJECT=["+m.title()+"] "+hdrSafe(m.Subject), "APM_MAIL_HTML="+m.HTML(), "APM_MAIL_ACCOUNT="+c.Account)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Outlook ile gönderilemedi: %s", firstLine(truncate(strings.TrimSpace(string(out)), 300)))
	}
	return nil
}
