package browser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"appmon/internal/cdp"
)

// LoginField giriş ekranında bulunan bir alan.
type LoginField struct {
	Role     string   `json:"role"` // username, password, otp, captcha, select, checkbox, other
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Name     string   `json:"name,omitempty"`
	Selector string   `json:"selector"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// LoginAnalysis giriş ekranının çözümlemesidir.
type LoginAnalysis struct {
	URL       string       `json:"url"`
	Title     string       `json:"title"`
	Fields    []LoginField `json:"fields"`
	Submit    string       `json:"submit,omitempty"` // gönder düğmesinin metni
	SubmitSel string       `json:"submit_selector,omitempty"`
	SSO       []string     `json:"sso,omitempty"` // "Google ile giriş" gibi harici giriş düğmeleri
	Captcha   bool         `json:"captcha"`
	OTP       bool         `json:"otp"`
	NoPass    bool         `json:"no_password"` // parola alanı görünmüyor (ör. önce e-posta, sonra parola)
	LoggedIn  bool         `json:"logged_in"`   // sayfada giriş formu yok
}

const analyzeJS = `(() => {
  const vis = e => { const r = e.getBoundingClientRect(), cs = getComputedStyle(e); return r.width > 0 && r.height > 0 && cs.visibility !== 'hidden' && cs.display !== 'none'; };
  const txt = s => (s || '').replace(/\s+/g, ' ').trim().slice(0, 80);
  const esc = s => (window.CSS && CSS.escape ? CSS.escape(s) : s.replace(/[^\w-]/g, '\\$&'));
  const sel = e => {
    if (e.id && document.querySelectorAll('#' + esc(e.id)).length === 1) return '#' + esc(e.id);
    const n = e.getAttribute('name');
    if (n) { const q = e.tagName.toLowerCase() + '[name="' + n.replace(/"/g, '') + '"]'; if (document.querySelectorAll(q).length === 1) return q; }
    const t = e.getAttribute('type');
    if (t) { const q = e.tagName.toLowerCase() + '[type="' + t + '"]'; if (document.querySelectorAll(q).length === 1) return q; }
    return '';
  };
  const label = e => {
    let l = '';
    if (e.id) { const x = document.querySelector('label[for="' + e.id.replace(/"/g, '') + '"]'); if (x) l = x.innerText; }
    if (!l) { const x = e.closest('label'); if (x) l = x.innerText; }
    if (!l) l = e.getAttribute('aria-label') || '';
    if (!l && e.getAttribute('aria-labelledby')) { const x = document.getElementById(e.getAttribute('aria-labelledby')); if (x) l = x.innerText; }
    if (!l) l = e.getAttribute('placeholder') || '';
    if (!l) { const p = e.previousElementSibling; if (p && /^(LABEL|SPAN|DIV|P)$/.test(p.tagName)) l = p.innerText; }
    return txt(l) || txt(e.getAttribute('name')) || txt(e.id);
  };
  const hay = e => ((e.getAttribute('name') || '') + ' ' + (e.id || '') + ' ' + (e.getAttribute('autocomplete') || '') + ' ' + (e.getAttribute('placeholder') || '') + ' ' + (e.getAttribute('aria-label') || '')).toLowerCase();
  const fields = [];
  let firstText = true;
  for (const e of document.querySelectorAll('input, select, textarea')) {
    const type = (e.getAttribute('type') || (e.tagName === 'SELECT' ? 'select' : 'text')).toLowerCase();
    if (['hidden', 'submit', 'button', 'image', 'reset'].includes(type) || !vis(e)) continue;
    const h = hay(e);
    let role = 'other';
    if (type === 'password') role = 'password';
    else if (type === 'checkbox' || type === 'radio') role = 'checkbox';
    else if (e.tagName === 'SELECT') role = 'select';
    else if (/otp|one-time|2fa|totp|sms|verification|doğrulama kodu|dogrulama|authenticator|mfa|pin/.test(h) || e.getAttribute('autocomplete') === 'one-time-code') role = 'otp';
    else if (/captcha|robot/.test(h)) role = 'captcha';
    else if (/user|login|email|e-posta|eposta|mail|kullan|tckn|tc\b|phone|tel|ad\b|account|hesap/.test(h) || (firstText && ['text', 'email', 'tel', 'search'].includes(type))) role = 'username';
    if (role === 'username') firstText = false;
    const f = { role, label: label(e), type, name: e.getAttribute('name') || '', selector: sel(e), required: e.required || e.getAttribute('aria-required') === 'true' };
    if (e.tagName === 'SELECT') f.options = [...e.options].slice(0, 12).map(o => txt(o.text)).filter(Boolean);
    fields.push(f);
  }
  const html = document.documentElement.innerHTML.toLowerCase();
  const captcha = !!document.querySelector('.g-recaptcha, .h-captcha, [data-sitekey], iframe[src*="recaptcha"], iframe[src*="hcaptcha"], iframe[src*="turnstile"], img[src*="captcha" i], [class*="captcha" i]') || fields.some(f => f.role === 'captcha');
  const sso = [];
  for (const b of document.querySelectorAll('a, button, [role=button]')) {
    const t = txt(b.innerText || b.getAttribute('aria-label'));
    if (vis(b) && t && /(google|microsoft|azure|apple|facebook|github|okta|saml|sso|e-devlet|edevlet|ldap|kurumsal)/i.test(t) && /(giriş|oturum|login|sign|ile|with|devam|continue)/i.test(t)) sso.push(t);
  }
  const pw = [...document.querySelectorAll('input[type=password]')].find(vis);
  let submit = null;
  const scope = pw ? (pw.form || document) : document;
  submit = [...scope.querySelectorAll('button, input[type=submit], [role=button]')].find(e => vis(e) && (e.type === 'submit' || !e.getAttribute('type') || e.tagName === 'BUTTON') && !/(google|microsoft|apple|facebook|github|sso)/i.test(e.innerText || ''));
  return { title: document.title, fields, captcha, sso: [...new Set(sso)].slice(0, 5), submit: submit ? txt(submit.innerText || submit.value || submit.getAttribute('aria-label')) : '', submit_selector: submit ? sel(submit) : '', no_password: !pw, otp: fields.some(f => f.role === 'otp') || /(doğrulama kodu|verification code|one-time|tek kullanımlık)/.test(html) && !!fields.length && !pw };
})()`

// AnalyzeLogin verilen adresi açar ve giriş ekranındaki alanları, düğmeleri ve engelleri (captcha, SSO, OTP) çıkarır.
// Hiçbir veri gönderilmez.
func AnalyzeLogin(ctx context.Context, o cdp.Options, rawURL string) (*LoginAnalysis, error) {
	if err := ValidURL(rawURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	b, err := cdp.Launch(ctx, o)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	p, err := NewPage(ctx, b.Page)
	if err != nil {
		return nil, err
	}
	if err := p.Navigate(ctx, rawURL); err != nil {
		return nil, fmt.Errorf("sayfa açılamadı: %w", err)
	}
	p.WaitIdle(ctx, time.Now(), 800*time.Millisecond, 10*time.Second)
	var res LoginAnalysis
	if err := p.Eval(ctx, analyzeJS, &res); err != nil {
		return nil, errors.New("sayfa çözümlenemedi")
	}
	res.URL = p.URL()
	res.LoggedIn = len(res.Fields) == 0 && !res.NoPass
	if res.Fields == nil {
		res.Fields = []LoginField{}
	}
	return &res, nil
}
