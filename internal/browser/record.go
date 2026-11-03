package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"appmon/internal/cdp"
)

const (
	MaxSessions   = 2
	IdleTimeout   = 10 * time.Minute
	MaxSessionAge = 30 * time.Minute
)

var ErrBusy = errors.New("eşzamanlı kayıt oturumu sınırına ulaşıldı; açık bir kaydı bitirin")

// Session canlı bir kayıt oturumudur: kullanıcının panelden verdiği girdiler tarayıcıda uygulanır ve adıma dönüştürülür.
type Session struct {
	ID     string
	Tenant int64
	b      *cdp.Browser
	page   *Page

	mu       sync.Mutex
	steps    []Step
	secrets  map[string]string
	typeIdx  int // son adım "type" ise indeksi, değilse -1
	frame    []byte
	seq      int64
	notify   chan struct{}
	created  time.Time
	lastUse  time.Time
	closed   bool
	opMu     sync.Mutex // girdiler sırayla işlenir
	typedSel string
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	Chrome   cdp.Options
}

func NewManager(o cdp.Options) *Manager { return &Manager{sessions: map[string]*Session{}, Chrome: o} }

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) Start(ctx context.Context, tenant int64, startURL string) (*Session, error) {
	return m.StartAuth(ctx, tenant, startURL, nil)
}

// StartAuth kaydı başlatır; auth verilirse giriş, kayda eklenmeden önce otomatik yapılır (giriş adımı kaydedilmez,
// kayıt oturum açılmış sayfadan başlar).
func (m *Manager) StartAuth(ctx context.Context, tenant int64, startURL string, auth *Auth) (*Session, error) {
	if auth != nil && (startURL == "" || auth.Username == "" || auth.Password == "") {
		return nil, errors.New("kimlik doğrulama için başlangıç adresi, kullanıcı adı ve parola gerekir")
	}
	if startURL != "" {
		if err := ValidURL(startURL); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	if len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	for _, s := range m.sessions {
		if s.Tenant == tenant {
			m.mu.Unlock()
			return nil, errors.New("zaten açık bir kayıt oturumunuz var; önce onu bitirin")
		}
	}
	s := &Session{ID: newID(), Tenant: tenant, secrets: map[string]string{}, typeIdx: -1, notify: make(chan struct{}), created: time.Now(), lastUse: time.Now()}
	m.sessions[s.ID] = s // yer ayır
	m.mu.Unlock()

	bctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	b, err := cdp.Launch(bctx, m.Chrome)
	if err == nil {
		s.b = b
		s.page, err = NewPage(bctx, b.Page)
	}
	if err != nil {
		m.remove(s)
		if b != nil {
			b.Close()
		}
		return nil, err
	}
	s.startScreencast(bctx)
	if auth != nil {
		a := *auth
		a.Enabled, a.Mode = true, "form"
		if err := s.loginUnrecorded(ctx, startURL, &a); err != nil {
			m.remove(s)
			b.Close()
			return nil, err
		}
	} else if startURL != "" {
		if err := s.Input(ctx, Input{Type: "goto", URL: startURL}); err != nil {
			m.remove(s)
			b.Close()
			return nil, err
		}
	}
	return s, nil
}

func (m *Manager) remove(s *Session) {
	m.mu.Lock()
	delete(m.sessions, s.ID)
	m.mu.Unlock()
}

func (m *Manager) Get(id string, tenant int64) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.Tenant != tenant {
		return nil, false
	}
	s.mu.Lock()
	s.lastUse = time.Now()
	s.mu.Unlock()
	return s, true
}

// Stop oturumu kapatır ve kaydedilen adımları ile gizli değişkenleri döndürür.
func (m *Manager) Stop(id string, tenant int64) ([]Step, map[string]string, error) {
	s, ok := m.Get(id, tenant)
	if !ok {
		return nil, nil, errors.New("kayıt oturumu bulunamadı veya süresi doldu")
	}
	m.remove(s)
	steps, secrets := s.Close()
	return steps, secrets, nil
}

// Reap boşta kalan veya çok uzun süren oturumları kapatır.
func (m *Manager) Reap(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for id, s := range m.sessions {
				s.Close()
				delete(m.sessions, id)
			}
			m.mu.Unlock()
			return
		case <-t.C:
			m.mu.Lock()
			for id, s := range m.sessions {
				s.mu.Lock()
				stale := time.Since(s.lastUse) > IdleTimeout || time.Since(s.created) > MaxSessionAge
				s.mu.Unlock()
				if stale {
					s.Close()
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (s *Session) Close() ([]Step, map[string]string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.steps, s.secrets
	}
	s.closed = true
	steps, secrets := append([]Step{}, s.steps...), map[string]string{}
	for k, v := range s.secrets {
		secrets[k] = v
	}
	close(s.notify)
	s.mu.Unlock()
	if s.b != nil {
		s.b.Close()
	}
	return steps, secrets
}

func (s *Session) startScreencast(ctx context.Context) {
	c := s.b.Page
	c.On("Page.screencastFrame", func(raw json.RawMessage) {
		var e struct {
			Data      []byte `json:"data"`
			SessionID int    `json:"sessionId"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return
		}
		go c.Call(context.Background(), "Page.screencastFrameAck", map[string]any{"sessionId": e.SessionID}, nil)
		s.mu.Lock()
		if !s.closed {
			s.frame = e.Data
			s.seq++
			old := s.notify
			s.notify = make(chan struct{})
			close(old)
		}
		s.mu.Unlock()
	})
	c.Call(ctx, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": 65, "maxWidth": ViewW, "maxHeight": ViewH, "everyNthFrame": 1}, nil)
}

// Frame, after'dan yeni bir kare gelene kadar (en çok wait) bekler ve son kareyi döndürür.
func (s *Session) Frame(ctx context.Context, after int64, wait time.Duration) ([]byte, int64, bool) {
	s.mu.Lock()
	if s.seq <= after && !s.closed {
		ch := s.notify
		s.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(wait):
		case <-ctx.Done():
		}
		s.mu.Lock()
	}
	defer s.mu.Unlock()
	if s.seq <= after || s.frame == nil {
		return nil, s.seq, s.closed
	}
	return s.frame, s.seq, s.closed
}

type State struct {
	URL     string   `json:"url"`
	Steps   []string `json:"steps"`
	Seq     int64    `json:"seq"`
	Expires int64    `json:"expires_in"`
}

func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{URL: s.page.URL(), Seq: s.seq, Expires: int64((IdleTimeout - time.Since(s.lastUse)).Seconds())}
	for _, x := range s.steps {
		st.Steps = append(st.Steps, x.Name)
	}
	return st
}

// Input panelden gelen bir kullanıcı girdisidir.
type Input struct {
	Type string  `json:"type"` // goto, click, text, key, scroll
	URL  string  `json:"url"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	Text string  `json:"text"`
	Key  string  `json:"key"`
}

func (s *Session) add(st Step) {
	s.mu.Lock()
	s.steps = append(s.steps, st)
	s.mu.Unlock()
}

func hostPath(raw string) string { return shortURL(raw) }

// Input girdiyi tarayıcıda uygular ve gerekirse bir adım olarak kaydeder.
func (s *Session) Input(ctx context.Context, in Input) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	closed, n := s.closed, len(s.steps)
	s.lastUse = time.Now()
	s.mu.Unlock()
	if closed {
		return errors.New("oturum kapalı")
	}
	if n >= MaxSteps {
		return fmt.Errorf("en fazla %d adım kaydedilebilir", MaxSteps)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p := s.page
	switch in.Type {
	case "goto":
		if err := ValidURL(in.URL); err != nil {
			return err
		}
		if err := p.Navigate(ctx, in.URL); err != nil {
			return err
		}
		p.WaitIdle(ctx, time.Now(), 500*time.Millisecond, 10*time.Second)
		s.typeIdx = -1
		s.add(Step{Name: "Aç: " + hostPath(in.URL), Action: "goto", URL: in.URL})
	case "click":
		info, _ := p.DescribeAt(ctx, in.X, in.Y)
		if err := p.mouse(ctx, in.X, in.Y); err != nil {
			return err
		}
		p.WaitIdle(ctx, time.Now(), 400*time.Millisecond, 5*time.Second)
		s.typeIdx = -1
		st := Step{Action: "click", X: int(in.X), Y: int(in.Y)}
		if info != nil {
			st.Selector, st.Text, st.Tag = info.Selector, info.Text, info.Tag
		}
		switch {
		case st.Text != "":
			st.Name = "Tıkla: " + st.Text
		case info != nil && info.Label != "":
			st.Name = "Tıkla: " + info.Label
		case st.Selector != "":
			st.Name = "Tıkla: " + st.Selector
		default:
			st.Name = fmt.Sprintf("Tıkla (%d,%d)", st.X, st.Y)
		}
		s.add(st)
	case "scroll":
		return p.Wheel(ctx, in.X, in.Y, in.DX, in.DY)
	case "text":
		if in.Text == "" || utf8.RuneCountInString(in.Text) > 2000 {
			return nil
		}
		info, _ := p.DescribeActive(ctx)
		if info == nil || !info.Editable {
			return nil
		}
		if err := p.InsertText(ctx, in.Text); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.typeIdx >= 0 && s.typeIdx < len(s.steps) && s.typedSel == info.Selector {
			st := &s.steps[s.typeIdx]
			if st.Secret {
				v := s.secrets[secretKey(st.Value)] + in.Text
				s.secrets[secretKey(st.Value)] = v
			} else {
				st.Value += in.Text
			}
			return nil
		}
		st := Step{Action: "type", Selector: info.Selector, Tag: info.Tag}
		label := info.Label
		if label == "" {
			label = info.Selector
		}
		if info.Password {
			name := fmt.Sprintf("secret_%d", len(s.secrets)+1)
			s.secrets[name] = in.Text
			st.Secret, st.Value, st.Name = true, "{{"+name+"}}", "Yaz (gizli): "+label
		} else {
			st.Value, st.Name = in.Text, "Yaz: "+label
		}
		s.steps = append(s.steps, st)
		s.typeIdx, s.typedSel = len(s.steps)-1, info.Selector
	case "key":
		if _, ok := Keys[in.Key]; !ok {
			return nil
		}
		if in.Key == "Backspace" {
			s.mu.Lock()
			if s.typeIdx >= 0 && s.typeIdx < len(s.steps) {
				st := &s.steps[s.typeIdx]
				if st.Secret {
					k := secretKey(st.Value)
					if v := s.secrets[k]; v != "" {
						_, sz := utf8.DecodeLastRuneInString(v)
						s.secrets[k] = v[:len(v)-sz]
					}
				} else if st.Value != "" {
					_, sz := utf8.DecodeLastRuneInString(st.Value)
					st.Value = st.Value[:len(st.Value)-sz]
				}
				s.mu.Unlock()
				return p.Press(ctx, "Backspace")
			}
			s.mu.Unlock()
		}
		if err := p.Press(ctx, in.Key); err != nil {
			return err
		}
		if in.Key == "Enter" || in.Key == "Tab" {
			p.WaitIdle(ctx, time.Now(), 400*time.Millisecond, 5*time.Second)
		}
		s.typeIdx = -1
		s.add(Step{Name: "Tuş: " + in.Key, Action: "press", Key: in.Key})
	default:
		return errors.New("geçersiz girdi türü")
	}
	return nil
}

func secretKey(v string) string { return strings.Trim(v, "{}") }

// loginUnrecorded giriş sayfasını açar, gerekiyorsa formu doldurur ve kayıt başlangıç adımını oturum açılmış sayfa olarak ekler.
func (s *Session) loginUnrecorded(ctx context.Context, startURL string, a *Auth) error {
	if err := ValidURL(startURL); err != nil {
		return err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	p := s.page
	if err := p.Navigate(ctx, startURL); err != nil {
		return err
	}
	p.WaitIdle(ctx, time.Now(), 500*time.Millisecond, 10*time.Second)
	needs, err := p.needsLogin(ctx, a)
	if err != nil {
		return errors.New("sayfa denetlenemedi")
	}
	if needs {
		if err := p.formLogin(ctx, a, map[string]string{}, 10*time.Second); err != nil {
			return fmt.Errorf("giriş yapılamadı: %w", err)
		}
		p.WaitIdle(ctx, time.Now(), 500*time.Millisecond, 10*time.Second)
		still, err := p.needsLogin(ctx, a)
		for i := 0; err == nil && still && i < 10; i++ { // yönlendirme geç tamamlanabilir
			time.Sleep(500 * time.Millisecond)
			still, err = p.needsLogin(ctx, a)
		}
		if err != nil || still {
			return errors.New("giriş başarısız (kullanıcı adı/parola hatalı olabilir veya alan seçicileri gerekebilir)")
		}
	}
	cur := p.URL()
	if cur == "" {
		cur = startURL
	}
	s.add(Step{Name: "Aç: " + hostPath(cur), Action: "goto", URL: cur})
	return nil
}
