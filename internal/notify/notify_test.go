package notify

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var testMsg = Message{Event: "open", Severity: "critical", Subject: "Disk dolmak üzere", Asset: "ana-makine", Rule: "Disk dolmak üzere", Value: "93 (> 90)", When: time.Now(), Link: "http://x/#alarmlar"}

// miniSMTP bir test SMTP sunucusudur (AUTH PLAIN/LOGIN, düz bağlantı).
type miniSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	mail     string
	rcpts    []string
	data     string
	user     string
	pass     string
	wantAuth bool
}

func newSMTP(t *testing.T, wantAuth bool) *miniSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &miniSMTP{ln: ln, wantAuth: wantAuth}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *miniSMTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *miniSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(x string) { c.Write([]byte(x + "\r\n")) }
	w("220 test ESMTP")
	authed := !s.wantAuth
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		l := strings.TrimSpace(line)
		u := strings.ToUpper(l)
		switch {
		case strings.HasPrefix(u, "EHLO"):
			c.Write([]byte("250-test\r\n250-AUTH LOGIN\r\n250 8BITMIME\r\n"))
		case strings.HasPrefix(u, "AUTH LOGIN"):
			w("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			a, _ := r.ReadString('\n')
			w("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			b, _ := r.ReadString('\n')
			ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(a))
			pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(b))
			s.mu.Lock()
			s.user, s.pass = string(ub), string(pb)
			s.mu.Unlock()
			if string(ub) == "kul" && string(pb) == "parola" {
				authed = true
				w("235 ok")
			} else {
				w("535 hatali")
			}
		case strings.HasPrefix(u, "MAIL FROM"):
			if !authed {
				w("530 auth gerekli")
				continue
			}
			s.mu.Lock()
			s.mail = l
			s.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(u, "RCPT TO"):
			s.mu.Lock()
			s.rcpts = append(s.rcpts, l)
			s.mu.Unlock()
			w("250 ok")
		case u == "DATA":
			w("354 devam")
			var b strings.Builder
			for {
				d, err := r.ReadString('\n')
				if err != nil || d == ".\r\n" {
					break
				}
				b.WriteString(d)
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			w("250 kuyrukta")
		case u == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSMTPPlain(t *testing.T) {
	s := newSMTP(t, true)
	t0 := time.Now()
	err := Send(context.Background(), "smtp", Config{Host: "127.0.0.1", Port: s.port(), Security: "none", User: "kul", Pass: "parola", From: "APP_MON <izleme@ornek.com>", To: "a@ornek.com, b@ornek.com"}, testMsg, Env{})
	if err != nil {
		t.Fatal(err)
	}
	_ = t0
	s.mu.Lock()
	mail, rcpts, data := s.mail, append([]string{}, s.rcpts...), s.data
	s.mu.Unlock()
	if !strings.Contains(mail, "izleme@ornek.com") || len(rcpts) != 2 {
		t.Fatalf("mail=%q rcpts=%v", mail, rcpts)
	}
	if !strings.Contains(data, "multipart/alternative") || !strings.Contains(data, "Subject: =?UTF-8?B?") {
		t.Fatalf("gövde beklenmedik:\n%s", data)
	}
	err = Send(context.Background(), "smtp", Config{Host: "127.0.0.1", Port: s.port(), Security: "none", User: "kul", Pass: "yanlis", From: "x@y.com", To: "a@b.com"}, testMsg, Env{})
	if err == nil || !strings.Contains(err.Error(), "kimlik doğrulama") {
		t.Fatalf("yanlış parola hata vermeli: %v", err)
	}
}

func TestGraph(t *testing.T) {
	var got struct {
		sync.Mutex
		auth, path, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			if !strings.Contains(string(b), "client_secret=gizli") {
				w.WriteHeader(400)
				w.Write([]byte(`{"error_description":"AADSTS7000215: gecersiz sir"}`))
				return
			}
			w.Write([]byte(`{"access_token":"TOK123"}`))
			return
		}
		got.Lock()
		got.auth, got.path, got.body = r.Header.Get("Authorization"), r.URL.Path, string(b)
		got.Unlock()
		w.WriteHeader(202)
	}))
	defer srv.Close()
	c := Config{TenantID: "tnt", ClientID: "cid", ClientSecret: "gizli", From: "izleme@ornek.com", To: "a@ornek.com", LoginURL: srv.URL, GraphURL: srv.URL}
	if err := Send(context.Background(), "graph", c, testMsg, Env{}); err != nil {
		t.Fatal(err)
	}
	got.Lock()
	if got.auth != "Bearer TOK123" || !strings.HasSuffix(got.path, "/sendMail") || !strings.Contains(got.body, "a@ornek.com") {
		t.Fatalf("auth=%q path=%q body=%q", got.auth, got.path, got.body)
	}
	got.Unlock()
	c.ClientSecret = "yanlis"
	if err := Send(context.Background(), "graph", c, testMsg, Env{}); err == nil || !strings.Contains(err.Error(), "AADSTS7000215") {
		t.Fatalf("beklenen kimlik hatası: %v", err)
	}
}

func TestEWS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "kul" || p != "parola" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "a@ornek.com") || !strings.Contains(string(b), "SendOnly") {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte(`<m:CreateItemResponse><m:ResponseMessages><m:CreateItemResponseMessage ResponseClass="Success"/></m:ResponseMessages></m:CreateItemResponse>`))
	}))
	defer srv.Close()
	c := Config{URL: srv.URL + "/EWS/Exchange.asmx", User: "kul", Pass: "parola", To: "a@ornek.com"}
	if err := Send(context.Background(), "ews", c, testMsg, Env{}); err != nil {
		t.Fatal(err)
	}
	c.Pass = "x"
	if err := Send(context.Background(), "ews", c, testMsg, Env{}); err == nil || !strings.Contains(err.Error(), "reddedildi") {
		t.Fatalf("%v", err)
	}
}

func TestWebhooks(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+" "+string(b))
		mu.Unlock()
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	for _, k := range []string{"webhook", "slack", "teams"} {
		if err := Send(context.Background(), k, Config{URL: srv.URL + "/" + k}, testMsg, Env{}); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if err := Send(context.Background(), "telegram", Config{Token: "123:ABC", ChatID: "42", URL: srv.URL}, testMsg, Env{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 || !strings.Contains(bodies[3], "/bot123:ABC/sendMessage") || !strings.Contains(bodies[3], `"chat_id":"42"`) {
		t.Fatalf("%v", bodies)
	}
	if !strings.Contains(bodies[0], `"severity":"critical"`) || !strings.Contains(bodies[2], "MessageCard") {
		t.Fatalf("%v", bodies)
	}
}

func TestValidateAndMAPI(t *testing.T) {
	if Validate("smtp", Config{Host: "h", From: "a@b.c", To: "bozuk"}) == nil {
		t.Error("geçersiz alıcı kabul edildi")
	}
	if Validate("graph", Config{}) == nil || Validate("telegram", Config{}) == nil || Validate("nope", Config{}) == nil {
		t.Error("eksik ayar kabul edildi")
	}
	err := Send(context.Background(), "mapi", Config{To: "a@b.c"}, testMsg, Env{})
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Errorf("Linux'ta MAPI açıklayıcı hata vermeli: %v", err)
	}
	if strings.Contains(testMsg.HTML(), "<script") || !strings.Contains(testMsg.HTML(), "#e01b1b") {
		t.Error("HTML gövde beklenmedik")
	}
	m := testMsg
	m.Subject = "<img src=x onerror=1>"
	if strings.Contains(m.HTML(), "<img") {
		t.Error("HTML kaçışı eksik")
	}
	_ = strconv.Itoa
}
