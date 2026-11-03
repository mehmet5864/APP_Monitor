package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"appmon/internal/cdp"
)

type HostStat struct {
	Requests int `json:"requests"`
	Failed   int `json:"failed"`
	Errors   int `json:"errors"` // HTTP 4xx/5xx yanıtları
}

// Page bir Chromium sayfasını ve ağ durumunu izleyen sarmalayıcıdır.
type Page struct {
	c *cdp.Client

	mu        sync.Mutex
	inflight  map[string]string // requestId -> kaynak türü
	reqHost   map[string]string
	lastNet   time.Time
	hosts     map[string]*HostStat
	mainFrame string
	docStatus int
	docErr    string
	curURL    string
}

const initScript = `
(() => {
  document.addEventListener('click', e => { const a = e.target && e.target.closest && e.target.closest('a[target]'); if (a) a.target = '_self'; }, true);
  window.open = u => { if (u) location.href = u; return null; };
})();`

func NewPage(ctx context.Context, c *cdp.Client) (*Page, error) {
	p := &Page{c: c, inflight: map[string]string{}, reqHost: map[string]string{}, hosts: map[string]*HostStat{}, lastNet: time.Now()}
	c.On("Network.requestWillBeSent", func(raw json.RawMessage) {
		var e struct {
			RequestID string               `json:"requestId"`
			Type      string               `json:"type"`
			Request   struct{ URL string } `json:"request"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return
		}
		u, err := url.Parse(e.Request.URL)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.lastNet = time.Now()
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		if e.Type != "EventSource" && e.Type != "WebSocket" && e.Type != "Ping" {
			p.inflight[e.RequestID] = e.Type
		}
		if _, seen := p.reqHost[e.RequestID]; !seen {
			p.reqHost[e.RequestID] = u.Host
			h := p.hosts[u.Host]
			if h == nil {
				h = &HostStat{}
				p.hosts[u.Host] = h
			}
			h.Requests++
		}
	})
	c.On("Network.responseReceived", func(raw json.RawMessage) {
		var e struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			FrameID   string `json:"frameId"`
			Response  struct {
				Status int `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if e.Response.Status >= 400 {
			if h := p.hosts[p.reqHost[e.RequestID]]; h != nil {
				h.Errors++
			}
		}
		if e.Type == "Document" && (e.FrameID == p.mainFrame || p.mainFrame == "") {
			p.docStatus = e.Response.Status
		}
	})
	done := func(failed bool) func(json.RawMessage) {
		return func(raw json.RawMessage) {
			var e struct {
				RequestID string `json:"requestId"`
				Type      string `json:"type"`
				ErrorText string `json:"errorText"`
				Canceled  bool   `json:"canceled"`
			}
			if json.Unmarshal(raw, &e) != nil {
				return
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			delete(p.inflight, e.RequestID)
			p.lastNet = time.Now()
			if failed && !e.Canceled && e.ErrorText != "net::ERR_ABORTED" {
				if h := p.hosts[p.reqHost[e.RequestID]]; h != nil {
					h.Failed++
				}
				if e.Type == "Document" {
					p.docErr = e.ErrorText
				}
			}
		}
	}
	c.On("Network.loadingFinished", done(false))
	c.On("Network.loadingFailed", done(true))
	c.On("Page.frameNavigated", func(raw json.RawMessage) {
		var e struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Frame.ParentID == "" {
			p.mu.Lock()
			p.mainFrame, p.curURL = e.Frame.ID, e.Frame.URL
			// Eski belgenin yarım kalan istekleri için Chrome bitiş olayı göndermeyebilir; yeni belge dışındakileri bırak.
			for id, typ := range p.inflight {
				if typ != "Document" {
					delete(p.inflight, id)
				}
			}
			p.mu.Unlock()
		}
	})
	c.On("Page.javascriptDialogOpening", func(json.RawMessage) {
		go c.Call(context.Background(), "Page.handleJavaScriptDialog", map[string]any{"accept": true}, nil)
	})
	for _, m := range []string{"Page.enable", "Network.enable", "Runtime.enable"} {
		if err := c.Call(ctx, m, nil, nil); err != nil {
			return nil, err
		}
	}
	if err := c.Call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": ViewW, "height": ViewH, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
		return nil, err
	}
	c.Call(ctx, "Page.setDownloadBehavior", map[string]any{"behavior": "deny"}, nil)
	c.Call(ctx, "Browser.setDownloadBehavior", map[string]any{"behavior": "deny"}, nil)
	if err := c.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": initScript}, nil); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Page) URL() string { p.mu.Lock(); defer p.mu.Unlock(); return p.curURL }

func (p *Page) Hosts() map[string]HostStat {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]HostStat, len(p.hosts))
	for k, v := range p.hosts {
		out[k] = *v
	}
	return out
}

func (p *Page) resetDoc() {
	p.mu.Lock()
	p.docStatus, p.docErr = 0, ""
	p.mu.Unlock()
}

// docProblem son ana belge yanıtında/yüklemesinde sorun varsa açıklar.
func (p *Page) docProblem() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.docErr != "" {
		return "sayfa yüklenemedi: " + p.docErr
	}
	if p.docStatus >= 400 {
		return fmt.Sprintf("sayfa HTTP %d döndürdü", p.docStatus)
	}
	return ""
}

// Eval sayfada bir ifade çalıştırıp sonucu out'a çözer.
func (p *Page) Eval(ctx context.Context, expr string, out any) error {
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := p.c.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}, &r); err != nil {
		return err
	}
	if r.Exception != nil {
		return errors.New("sayfa betiği hata verdi")
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

// WaitIdle ağ etkinliği sessizleşene kadar bekler; son ağ etkinliğinin zamanını döndürür.
func (p *Page) WaitIdle(ctx context.Context, since time.Time, quiet, max time.Duration) time.Time {
	deadline := time.Now().Add(max)
	for {
		p.mu.Lock()
		n, last := len(p.inflight), p.lastNet
		p.mu.Unlock()
		if last.Before(since) {
			last = since // eylemden sonra en az "quiet" süre ağ hareketi beklenir (gezinme henüz başlamamış olabilir)
		}
		now := time.Now()
		if n == 0 && now.Sub(last) >= quiet {
			return last
		}
		if now.After(deadline) || ctx.Err() != nil {
			return now
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (p *Page) Navigate(ctx context.Context, u string) error {
	p.resetDoc()
	var r struct {
		ErrorText string `json:"errorText"`
	}
	if err := p.c.Call(ctx, "Page.navigate", map[string]any{"url": u}, &r); err != nil {
		return err
	}
	if r.ErrorText != "" {
		return errors.New("sayfa yüklenemedi: " + r.ErrorText)
	}
	return nil
}

func (p *Page) mouse(ctx context.Context, x, y float64) error {
	for _, ev := range []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1},
	} {
		if err := p.c.Call(ctx, "Input.dispatchMouseEvent", ev, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Page) Wheel(ctx context.Context, x, y, dx, dy float64) error {
	return p.c.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": x, "y": y, "deltaX": dx, "deltaY": dy}, nil)
}

func (p *Page) InsertText(ctx context.Context, s string) error {
	return p.c.Call(ctx, "Input.insertText", map[string]any{"text": s}, nil)
}

func (p *Page) Press(ctx context.Context, key string) error {
	k, ok := Keys[key]
	if !ok {
		return errors.New("desteklenmeyen tuş")
	}
	down := map[string]any{"type": "rawKeyDown", "key": key, "code": k.Code, "windowsVirtualKeyCode": k.VK}
	if k.Text != "" {
		down["type"], down["text"] = "keyDown", k.Text
	}
	if err := p.c.Call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return p.c.Call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": k.Code, "windowsVirtualKeyCode": k.VK}, nil)
}

type Point struct{ X, Y float64 }

// Resolve öğeyi bulur (önce CSS, sonra görünen metin), görünür alana kaydırır ve merkezini verir.
func (p *Page) Resolve(ctx context.Context, selector, text string, wait time.Duration) (*Point, error) {
	sel, _ := json.Marshal(selector)
	txt, _ := json.Marshal(text)
	expr := `(() => {
  const selector = ` + string(sel) + `, text = ` + string(txt) + `;
  let el = null;
  if (selector) { try { el = document.querySelector(selector); } catch (e) {} }
  if (!el && text) {
    const cands = [...document.querySelectorAll('a,button,input[type=submit],input[type=button],[role=button],[role=link],label,summary')];
    const t = x => (x.innerText || x.value || x.getAttribute('aria-label') || '').trim();
    el = cands.find(c => t(c) === text) || cands.find(c => t(c).includes(text));
  }
  if (!el) return null;
  el.scrollIntoView({block: 'center', inline: 'center'});
  const r = el.getBoundingClientRect();
  if (r.width === 0 || r.height === 0) return null;
  return {X: r.left + r.width / 2, Y: r.top + r.height / 2};
})()`
	deadline := time.Now().Add(wait)
	for {
		var pt *Point
		if err := p.Eval(ctx, expr, &pt); err == nil && pt != nil {
			return pt, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, errors.New("öğe bulunamadı")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (p *Page) ClickStep(ctx context.Context, st Step, wait time.Duration) error {
	pt, err := p.Resolve(ctx, st.Selector, st.Text, wait)
	if err != nil {
		if st.X != 0 || st.Y != 0 { // yedek: kayıt anındaki koordinat (aynı görünüm boyutu)
			return p.mouse(ctx, float64(st.X), float64(st.Y))
		}
		return err
	}
	return p.mouse(ctx, pt.X, pt.Y)
}

func (p *Page) TypeStep(ctx context.Context, selector, value string, wait time.Duration) error {
	if _, err := p.Resolve(ctx, selector, "", wait); err != nil {
		return err
	}
	sel, _ := json.Marshal(selector)
	if err := p.Eval(ctx, `(() => { const el = document.querySelector(`+string(sel)+`); el.focus(); if (el.select) el.select(); return true; })()`, nil); err != nil {
		return err
	}
	if value == "" {
		return p.Press(ctx, "Backspace")
	}
	return p.InsertText(ctx, value)
}

type ElemInfo struct {
	Selector string `json:"selector"`
	Text     string `json:"text"`
	Tag      string `json:"tag"`
	Label    string `json:"label"`
	Editable bool   `json:"editable"`
	Password bool   `json:"password"`
}

const infoJS = `
function __apmInfo(el) {
  if (!el || el.nodeType !== 1) return null;
  const esc = s => CSS.escape(s);
  const uniq = s => { try { return document.querySelectorAll(s).length === 1; } catch (e) { return false; } };
  const dyn = s => /\d{4,}|[0-9a-f]{8,}/i.test(s);
  let sel = '';
  if (el.id && !dyn(el.id) && uniq('#' + esc(el.id))) sel = '#' + esc(el.id);
  if (!sel) for (const a of ['data-testid', 'data-test', 'data-cy', 'name', 'aria-label', 'placeholder', 'title', 'alt']) {
    const v = el.getAttribute(a);
    if (v && !dyn(v)) { const s = el.tagName.toLowerCase() + '[' + a + '="' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"]'; if (uniq(s)) { sel = s; break; } }
  }
  if (!sel) {
    const parts = []; let e = el;
    while (e && e.nodeType === 1 && e !== document.body && parts.length < 6) {
      let p = e.tagName.toLowerCase();
      if (e.id && !dyn(e.id) && uniq('#' + esc(e.id))) { parts.unshift('#' + esc(e.id)); break; }
      const par = e.parentElement;
      if (par) { const same = [...par.children].filter(c => c.tagName === e.tagName); if (same.length > 1) p += ':nth-of-type(' + (same.indexOf(e) + 1) + ')'; }
      parts.unshift(p); e = par;
      if (uniq(parts.join(' > '))) break;
    }
    sel = parts.join(' > ');
  }
  const tag = el.tagName.toLowerCase();
  const type = (el.getAttribute('type') || '').toLowerCase();
  const text = (el.innerText || el.value || el.getAttribute('aria-label') || '').trim().replace(/\s+/g, ' ').slice(0, 60);
  const label = (el.getAttribute('placeholder') || el.getAttribute('aria-label') || el.getAttribute('name') || el.id || (el.labels && el.labels[0] && el.labels[0].innerText) || '').trim().slice(0, 40);
  const editable = tag === 'textarea' || el.isContentEditable || (tag === 'input' && !['button','submit','checkbox','radio','file','image','reset','range','color'].includes(type));
  return {selector: sel, text: tag === 'input' && type !== 'submit' && type !== 'button' ? '' : text, tag, label, editable, password: type === 'password'};
}`

func (p *Page) DescribeAt(ctx context.Context, x, y float64) (*ElemInfo, error) {
	var info *ElemInfo
	err := p.Eval(ctx, fmt.Sprintf(`(() => { %s
  const el = document.elementFromPoint(%f, %f);
  if (!el) return null;
  return __apmInfo(el.closest('a,button,input,select,textarea,label,summary,[role=button],[role=link],[onclick]') || el);
})()`, infoJS, x, y), &info)
	return info, err
}

func (p *Page) DescribeActive(ctx context.Context) (*ElemInfo, error) {
	var info *ElemInfo
	err := p.Eval(ctx, `(() => { `+infoJS+` const a = document.activeElement; return a && a !== document.body ? __apmInfo(a) : null; })()`, &info)
	return info, err
}

// dynSelector dönen/değişen bileşenleri (slayt gösterisi, ziyaretçi sayacı, saat, reklam...) tanımlar; parmak izine girmez.
const dynSelector = `[class*="carousel" i],[class*="slider" i],[class*="slideshow" i],[class*="swiper" i],[class*="slick" i],[class*="ticker" i],` +
	`[class*="marquee" i],[class*="counter" i],[class*="visitor" i],[class*="rotator" i],[class*="countdown" i],[class*="clock" i],[class*="timer" i],` +
	`[class*="advert" i],[class*="ads-" i],[id*="carousel" i],[id*="slider" i],[id*="counter" i],[id*="visitor" i],[id*="clock" i],` +
	`[aria-live],[aria-roledescription="carousel"],[data-ride],[data-slick-index],marquee,iframe,ins.adsbygoogle`

// Fingerprint sayfanın yapısal parmak izini çıkarır. Değişken içerik bilerek dışarıda kalır: dinamik bileşenler
// (dynSelector), sayılar (ziyaretçi sayısı, fiyat, tarih) '#' olarak normalleştirilir.
func (p *Page) Fingerprint(ctx context.Context) ([]string, error) {
	var items []string
	err := p.Eval(ctx, `(() => {
  const out = new Set();
  const DYN = `+jsQuote(dynSelector)+`;
  const dyn = e => { try { return !!e.closest(DYN); } catch (x) { return false; } };
  const norm = s => s.replace(/\d+([.,:]\d+)*/g, '#');
  const cut = (s, n) => norm((s || '').trim().replace(/\s+/g, ' ')).slice(0, n || 60);
  out.add('title:' + cut(document.title, 80));
  document.querySelectorAll('h1,h2,h3').forEach(e => { if (dyn(e)) return; const t = cut(e.innerText); if (t) out.add('h:' + e.tagName.toLowerCase() + ':' + t); });
  document.querySelectorAll('form').forEach(f => { if (dyn(f)) return; let p = ''; try { p = new URL(f.action, location.href).pathname; } catch (e) {} out.add('form:' + (f.method || 'get') + ':' + norm(p)); });
  document.querySelectorAll('input,select,textarea').forEach(e => { if ((e.type || '') === 'hidden' || dyn(e)) return; out.add('in:' + e.tagName.toLowerCase() + ':' + (e.type || '') + ':' + cut(e.name || e.id || e.getAttribute('placeholder'), 40)); });
  document.querySelectorAll('button,[role=button],input[type=submit]').forEach(e => { if (dyn(e)) return; const t = cut(e.innerText || e.value || e.getAttribute('aria-label'), 40); if (t) out.add('btn:' + t); });
  document.querySelectorAll('a[href]').forEach(a => { if (dyn(a)) return; try { const u = new URL(a.href, location.href); if (u.protocol.startsWith('http')) out.add('a:' + (u.host === location.host ? '' : u.host) + norm(u.pathname)); } catch (e) {} });
  document.querySelectorAll('script[src]').forEach(s => { try { out.add('script:' + new URL(s.src, location.href).host); } catch (e) {} });
  return [...out].sort().slice(0, 300);
})()`, &items)
	return items, err
}

// StableFingerprint iki örneği kısa aralıkla alıp yalnızca ikisinde de bulunan öğeleri döndürür;
// sayfa içinde kendiliğinden dönen/yenilenen öğeler böylece elenir.
func (p *Page) StableFingerprint(ctx context.Context, gap time.Duration) ([]string, error) {
	a, err := p.Fingerprint(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case <-time.After(gap):
	case <-ctx.Done():
		return a, nil
	}
	b, err := p.Fingerprint(ctx)
	if err != nil {
		return a, nil
	}
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := a[:0:0]
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out, nil
}

func (p *Page) BodyText(ctx context.Context) (string, error) {
	var s string
	err := p.Eval(ctx, `document.body ? document.body.innerText : ''`, &s)
	return s, err
}

// Shot küçültülmüş bir JPEG ekran görüntüsü alır.
func (p *Page) Shot(ctx context.Context) ([]byte, error) {
	var r struct {
		Data []byte `json:"data"` // base64 -> []byte çözümü encoding/json tarafından yapılır
	}
	err := p.c.Call(ctx, "Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": 45,
		"clip": map[string]any{"x": 0, "y": 0, "width": ViewW, "height": ViewH, "scale": 0.6}}, &r)
	return r.Data, err
}

func shortURL(s string) string {
	if u, err := url.Parse(s); err == nil {
		return strings.TrimSuffix(u.Host+u.Path, "/")
	}
	return s
}

// ---- Oturum / giriş ----

// needsLogin sayfanın giriş gerektirip gerektirmediğini söyler.
func (p *Page) needsLogin(ctx context.Context, a *Auth) (bool, error) {
	if a.LoggedInText != "" {
		txt, err := p.BodyText(ctx)
		if err != nil {
			return false, err
		}
		return !strings.Contains(txt, a.LoggedInText), nil
	}
	var visible bool
	err := p.Eval(ctx, `[...document.querySelectorAll('input[type=password]')].some(e => {
  const r = e.getBoundingClientRect(), cs = getComputedStyle(e);
  return r.width > 0 && r.height > 0 && cs.visibility !== 'hidden' && cs.display !== 'none';
})`, &visible)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	unauth := p.docStatus == 401 || p.docStatus == 403
	p.mu.Unlock()
	return visible || unauth, nil
}

const markLoginJS = `(() => {
  const vis = e => { const r = e.getBoundingClientRect(), cs = getComputedStyle(e); return r.width > 0 && r.height > 0 && cs.visibility !== 'hidden' && cs.display !== 'none'; };
  document.querySelectorAll('[data-apm]').forEach(e => e.removeAttribute('data-apm'));
  const sel = %s;
  const pick = (s) => { if (!s) return null; try { return document.querySelector(s); } catch (e) { return null; } };
  const pw = pick(sel.pass) || [...document.querySelectorAll('input[type=password]')].find(vis);
  if (!pw) return {error: 'giriş formunda parola alanı bulunamadı'};
  let user = pick(sel.user);
  if (!user) {
    const cands = [...document.querySelectorAll('input')].filter(e => vis(e) && ['text', 'email', 'tel', 'search', ''].includes((e.getAttribute('type') || '').toLowerCase()));
    for (const e of cands) if (e.compareDocumentPosition(pw) & Node.DOCUMENT_POSITION_FOLLOWING) user = e;
  }
  let submit = pick(sel.submit);
  if (!submit) {
    const scope = pw.form || document;
    submit = [...scope.querySelectorAll('button, input[type=submit], [role=button]')].find(e => vis(e) && (e.type === 'submit' || !e.getAttribute('type') || e.tagName === 'BUTTON')) || null;
  }
  pw.setAttribute('data-apm', 'p');
  if (user) user.setAttribute('data-apm', 'u');
  if (submit) submit.setAttribute('data-apm', 's');
  return {user: !!user, submit: !!submit};
})()`

// formLogin giriş formunu bulup kullanıcı adı ve parolayı otomatik doldurur.
func (p *Page) formLogin(ctx context.Context, a *Auth, vars map[string]string, wait time.Duration) error {
	sel, _ := json.Marshal(map[string]string{"user": a.UserSelector, "pass": a.PassSelector, "submit": a.SubmitSelector})
	var found struct {
		Error  string `json:"error"`
		User   bool   `json:"user"`
		Submit bool   `json:"submit"`
	}
	if err := p.Eval(ctx, fmt.Sprintf(markLoginJS, sel), &found); err != nil {
		return errors.New("giriş formu denetlenemedi")
	}
	if found.Error != "" {
		return errors.New(found.Error)
	}
	if found.User {
		if err := p.TypeStep(ctx, `[data-apm="u"]`, subst(a.Username, vars), wait); err != nil {
			return errors.New("kullanıcı adı alanı doldurulamadı")
		}
	}
	if err := p.TypeStep(ctx, `[data-apm="p"]`, subst(a.Password, vars), wait); err != nil {
		return errors.New("parola alanı doldurulamadı")
	}
	p.resetDoc()
	if found.Submit {
		if err := p.ClickStep(ctx, Step{Selector: `[data-apm="s"]`}, wait); err != nil {
			return errors.New("giriş düğmesine tıklanamadı")
		}
		return nil
	}
	return p.Press(ctx, "Enter")
}

// Cookies tarayıcıdaki çerezleri saklanabilir biçimde döndürür (süresi dolmamış, en çok 100).
func (p *Page) Cookies(ctx context.Context) []Cookie {
	var r struct {
		Cookies []struct {
			Name, Value, Domain, Path string
			Expires                   float64
			HTTPOnly                  bool `json:"httpOnly"`
			Secure                    bool
			SameSite                  string `json:"sameSite"`
		} `json:"cookies"`
	}
	if err := p.c.Call(ctx, "Network.getAllCookies", nil, &r); err != nil {
		return nil
	}
	out := []Cookie{}
	now := float64(time.Now().Unix())
	for _, c := range r.Cookies {
		if c.Expires > 0 && c.Expires < now {
			continue
		}
		if len(out) >= 100 || len(c.Value) > 4096 {
			continue
		}
		out = append(out, Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Expires: c.Expires,
			HTTPOnly: c.HTTPOnly, Secure: c.Secure, SameSite: c.SameSite})
	}
	return out
}

func (p *Page) SetCookies(ctx context.Context, cs []Cookie) error {
	list := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		m := map[string]any{"name": c.Name, "value": c.Value, "domain": c.Domain, "path": c.Path, "httpOnly": c.HTTPOnly, "secure": c.Secure}
		if c.Expires > 0 {
			m["expires"] = c.Expires
		}
		if c.SameSite != "" {
			m["sameSite"] = c.SameSite
		}
		list = append(list, m)
	}
	return p.c.Call(ctx, "Network.setCookies", map[string]any{"cookies": list}, nil)
}

// jsQuote bir Go dizesini JS dize sabitine çevirir.
func jsQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
