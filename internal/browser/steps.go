// Package browser gerçek bir (headless) Chromium ile kaydedilen kullanıcı akışlarını kaydeder, tekrar oynatır,
// adım sürelerini ölçer ve sayfa yapısındaki değişiklikleri ile ağ bağımlılıklarını çıkarır.
package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	ViewW, ViewH = 1280, 720
	MaxSteps     = 60
)

type Step struct {
	Name       string `json:"name"`
	Action     string `json:"action"` // goto, click, type, press
	URL        string `json:"url,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Text       string `json:"text,omitempty"` // tıklanan öğenin görünen metni (selector bulunamazsa yedek)
	Tag        string `json:"tag,omitempty"`
	X          int    `json:"x,omitempty"`
	Y          int    `json:"y,omitempty"`
	Value      string `json:"value,omitempty"`
	Secret     bool   `json:"secret,omitempty"` // değer {{secret_N}} değişkenindedir
	Key        string `json:"key,omitempty"`
	ExpectText string `json:"expect_text,omitempty"`
	MaxMS      int    `json:"max_ms,omitempty"`
	Login      bool   `json:"login,omitempty"` // giriş bloğuna dahil: yalnızca oturum açılması gerekiyorsa çalışır
}

// Auth senaryonun kimlik doğrulama ayarıdır. Parola veritabanında düz metindir; API yanıtlarında gösterilmez.
type Auth struct {
	Enabled        bool   `json:"enabled"`
	Mode           string `json:"mode"` // "form": kullanıcı adı/parola ile otomatik doldur; "steps": Login işaretli adımları çalıştır
	CheckURL       string `json:"check_url,omitempty"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	UserSelector   string `json:"user_selector,omitempty"`
	PassSelector   string `json:"pass_selector,omitempty"`
	SubmitSelector string `json:"submit_selector,omitempty"`
	LoggedInText   string `json:"logged_in_text,omitempty"`
}

// Cookie saklanan oturum çerezidir.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

// ValidateAuth kimlik doğrulama ayarını adımlarla birlikte denetler.
func ValidateAuth(a *Auth, steps []Step) error {
	if a == nil || !a.Enabled {
		return nil
	}
	if a.Mode != "form" && a.Mode != "steps" {
		return errors.New("kimlik doğrulama modu form veya steps olmalı")
	}
	if len(a.Username) > 300 || len(a.Password) > 300 || len(a.UserSelector) > 500 || len(a.PassSelector) > 500 ||
		len(a.SubmitSelector) > 500 || len(a.LoggedInText) > 200 {
		return errors.New("kimlik doğrulama alanlarından biri çok uzun")
	}
	check := a.CheckURL
	if check == "" {
		for _, st := range steps {
			if st.Action == "goto" && !st.Login {
				check = st.URL
				break
			}
		}
	}
	if check == "" {
		return errors.New("giriş gerektiren sayfa adresi (check_url) gerekli")
	}
	if !strings.Contains(check, "{{") {
		if err := ValidURL(check); err != nil {
			return errors.New("check_url: " + err.Error())
		}
	}
	login, rest := 0, 0
	for _, st := range steps {
		if st.Login {
			login++
		} else {
			rest++
		}
	}
	if rest == 0 {
		return errors.New("giriş dışında en az bir adım gerekli")
	}
	if a.Mode == "form" && (a.Username == "" || a.Password == "") {
		return errors.New("kullanıcı adı ve parola gerekli")
	}
	if a.Mode == "steps" && login == 0 {
		return errors.New("giriş bloğu için en az bir adımı 'giriş' olarak işaretleyin")
	}
	return nil
}

var varRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var Keys = map[string]struct {
	Code string
	VK   int
	Text string
}{
	"Enter": {"Enter", 13, "\r"}, "Tab": {"Tab", 9, ""}, "Escape": {"Escape", 27, ""}, "Backspace": {"Backspace", 8, ""},
	"Delete": {"Delete", 46, ""}, "ArrowUp": {"ArrowUp", 38, ""}, "ArrowDown": {"ArrowDown", 40, ""},
	"ArrowLeft": {"ArrowLeft", 37, ""}, "ArrowRight": {"ArrowRight", 39, ""}, "Home": {"Home", 36, ""}, "End": {"End", 35, ""},
	"PageUp": {"PageUp", 33, ""}, "PageDown": {"PageDown", 34, ""},
}

func ValidURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("adres http:// veya https:// ile başlamalı")
	}
	if len(s) > 2000 {
		return errors.New("adres çok uzun")
	}
	return nil
}

// Validate tarayıcı adımlarını ve kullanılan değişkenleri denetler.
func Validate(steps []Step, vars map[string]string) error {
	if len(steps) == 0 || len(steps) > MaxSteps {
		return fmt.Errorf("adım sayısı 1 ile %d arasında olmalı", MaxSteps)
	}
	for k := range vars {
		if !varNameRe.MatchString(k) {
			return fmt.Errorf("geçersiz değişken adı: %q", k)
		}
	}
	for i := range steps {
		st := &steps[i]
		w := func(msg string) error { return fmt.Errorf("adım %d: %s", i+1, msg) }
		st.Name = strings.TrimSpace(st.Name)
		if st.Name == "" {
			st.Name = fmt.Sprintf("Adım %d", i+1)
		}
		if len(st.Name) > 100 || len(st.Selector) > 500 || len(st.Text) > 200 || len(st.Value) > 2000 || len(st.ExpectText) > 200 {
			return w("alanlardan biri çok uzun")
		}
		if st.MaxMS < 0 || st.MaxMS > 600000 {
			return w("en fazla süre geçersiz")
		}
		switch st.Action {
		case "goto":
			if err := ValidURL(st.URL); err != nil {
				return w(err.Error())
			}
		case "click":
			if st.Selector == "" && st.Text == "" && st.X == 0 && st.Y == 0 {
				return w("tıklanacak öğe tanımlı değil")
			}
		case "type":
			if st.Selector == "" {
				return w("yazılacak alan tanımlı değil")
			}
		case "press":
			if _, ok := Keys[st.Key]; !ok {
				return w("desteklenmeyen tuş")
			}
		default:
			return w("geçersiz eylem")
		}
		for _, f := range []string{st.URL, st.Value, st.ExpectText} {
			for _, m := range varRe.FindAllStringSubmatch(f, -1) {
				if _, ok := vars[m[1]]; !ok {
					return w("{{" + m[1] + "}} değişkeni tanımlı değil")
				}
			}
		}
	}
	return nil
}

func subst(s string, vars map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		return vars[varRe.FindStringSubmatch(m)[1]]
	})
}

// Summary adımın arayüzde gösterilecek, gizli değer içermeyen özeti.
func (s Step) Summary() string {
	switch s.Action {
	case "goto":
		if u, err := url.Parse(s.URL); err == nil {
			return "Aç " + u.Host + u.Path
		}
	case "click":
		if s.Text != "" {
			return "Tıkla “" + s.Text + "”"
		}
		return "Tıkla " + s.Selector
	case "type":
		if s.Secret {
			return "Yaz (gizli) → " + s.Selector
		}
		return "Yaz → " + s.Selector
	case "press":
		return "Tuş " + s.Key
	}
	return s.Action
}

// Diff iki parmak izi kümesi arasındaki eklenen ve kaybolan öğeleri verir (en çok 25'er).
func Diff(prev, cur []string) (added, removed []string) {
	p, c := map[string]bool{}, map[string]bool{}
	for _, x := range prev {
		p[x] = true
	}
	for _, x := range cur {
		c[x] = true
	}
	for _, x := range cur {
		if !p[x] && len(added) < 25 {
			added = append(added, x)
		}
	}
	for _, x := range prev {
		if !c[x] && len(removed) < 25 {
			removed = append(removed, x)
		}
	}
	return
}

func Hash(items []string) string {
	s := append([]string{}, items...)
	sort.Strings(s)
	sum := sha256.Sum256([]byte(strings.Join(s, "\n")))
	return hex.EncodeToString(sum[:8])
}
