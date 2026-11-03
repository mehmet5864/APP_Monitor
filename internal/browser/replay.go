package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"appmon/internal/cdp"
)

type Options struct {
	Chrome      cdp.Options
	StepTimeout time.Duration // öğe bekleme ve ağ durulma üst sınırı
	Shots       bool
	Auth        *Auth
	Cookies     []Cookie // saklanan oturum
	ForceLogin  bool     // saklanan oturumu yok say
}

type StepResult struct {
	Name   string   `json:"name"`
	OK     bool     `json:"ok"`
	MS     float64  `json:"ms"`
	Error  string   `json:"error,omitempty"`
	Page   string   `json:"page,omitempty"` // adımdan sonraki sayfa (host+yol)
	Items  []string `json:"-"`
	Hash   string   `json:"hash,omitempty"`
	Shot   []byte   `json:"-"`
	Added  []string `json:"added,omitempty"`
	Remove []string `json:"removed,omitempty"`
}

type Result struct {
	Steps       []StepResult
	Hosts       map[string]HostStat
	Duration    time.Duration
	Cookies     []Cookie // başarılı çalıştırma sonunda tarayıcıdaki oturum
	LoginFailed bool     // kimlik bilgileri gönderildi ama giriş tamamlanamadı (parola hatalı olabilir)
	Session     string   // "", "reused" (saklanan oturum geçerliydi) veya "login" (giriş yapıldı)
}

func (o Options) authOn() bool { return o.Auth != nil && o.Auth.Enabled }

// doStep tek bir adımın eylemini uygular (bekleme ve doğrulama çağıran tarafındadır).
func doStep(ctx context.Context, page *Page, st Step, vars map[string]string, o Options) error {
	page.resetDoc()
	var err error
	switch st.Action {
	case "goto":
		err = page.Navigate(ctx, subst(st.URL, vars))
	case "click":
		err = page.ClickStep(ctx, st, o.StepTimeout)
	case "type":
		err = page.TypeStep(ctx, st.Selector, subst(st.Value, vars), o.StepTimeout)
	case "press":
		err = page.Press(ctx, st.Key)
	}
	if err != nil && (st.Action == "click" || st.Action == "type") {
		return fmt.Errorf("%s (%s)", err.Error(), st.Summary())
	}
	return err
}

// Replay adımları sırayla oynatır. Kimlik doğrulama açıksa önce oturum denetlenir: saklanan oturum geçerliyse giriş
// atlanır, değilse giriş yapılır. İlk başarısız adımda durur ve hatayı döndürür.
func Replay(ctx context.Context, steps []Step, vars map[string]string, o Options) (Result, error) {
	var res Result
	if o.StepTimeout <= 0 {
		o.StepTimeout = 10 * time.Second
	}
	start := time.Now()
	b, err := cdp.Launch(ctx, o.Chrome)
	if err != nil {
		return res, err
	}
	defer b.Close()
	page, err := NewPage(ctx, b.Page)
	if err != nil {
		return res, err
	}
	finish := func() { res.Hosts, res.Duration = page.Hosts(), time.Since(start) }

	if o.authOn() {
		if err := authenticate(ctx, page, steps, vars, o, &res); err != nil {
			finish()
			return res, err
		}
	}

	for i, st := range steps {
		if st.Login && o.authOn() { // giriş adımları yalnızca giriş bloğunda çalışır
			continue
		}
		r := StepResult{Name: st.Name}
		t0 := time.Now()
		fail := func(msg string) (Result, error) {
			r.Error = msg
			r.MS = float64(time.Since(t0).Microseconds()) / 1000
			r.Page = shortURL(page.URL())
			if o.Shots {
				if sh, e := page.Shot(ctx); e == nil {
					r.Shot = sh
				}
			}
			res.Steps = append(res.Steps, r)
			finish()
			return res, fmt.Errorf("adım %d (%s): %s", i+1, st.Name, msg)
		}
		if aerr := doStep(ctx, page, st, vars, o); aerr != nil {
			return fail(aerr.Error())
		}
		last := page.WaitIdle(ctx, time.Now(), 500*time.Millisecond, o.StepTimeout)
		elapsed := last.Sub(t0)
		if elapsed < 0 {
			elapsed = time.Since(t0)
		}
		r.MS = float64(elapsed.Microseconds()) / 1000
		r.Page = shortURL(page.URL())
		if p := page.docProblem(); p != "" {
			return fail(p)
		}
		if st.ExpectText != "" {
			txt, _ := page.BodyText(ctx)
			if !strings.Contains(txt, subst(st.ExpectText, vars)) {
				return fail("beklenen metin sayfada bulunamadı")
			}
		}
		if st.MaxMS > 0 && elapsed > time.Duration(st.MaxMS)*time.Millisecond {
			return fail(fmt.Sprintf("adım süresi sınırı aşıldı (%d ms > %d ms)", elapsed.Milliseconds(), st.MaxMS))
		}
		if items, e := page.StableFingerprint(ctx, 600*time.Millisecond); e == nil {
			r.Items, r.Hash = items, Hash(items)
		}
		if o.Shots {
			if sh, e := page.Shot(ctx); e == nil {
				r.Shot = sh
			}
		}
		r.OK = true
		res.Steps = append(res.Steps, r)
		if ctx.Err() != nil {
			break
		}
	}
	finish()
	if ctx.Err() == nil && o.authOn() {
		res.Cookies = page.Cookies(ctx)
	}
	return res, ctx.Err()
}

// authenticate oturumu denetler; gerekirse giriş yapar. Sonucu res.Steps'in ilk öğesi olarak ekler.
func authenticate(ctx context.Context, page *Page, steps []Step, vars map[string]string, o Options, res *Result) error {
	a := o.Auth
	r := StepResult{Name: "Oturum denetimi"}
	t0 := time.Now()
	fail := func(msg string) error {
		r.Error, r.MS, r.Page = msg, float64(time.Since(t0).Microseconds())/1000, shortURL(page.URL())
		if o.Shots {
			if sh, e := page.Shot(ctx); e == nil {
				r.Shot = sh
			}
		}
		res.Steps = append(res.Steps, r)
		return fmt.Errorf("giriş: %s", msg)
	}
	check := subst(a.CheckURL, vars)
	if check == "" {
		for _, st := range steps {
			if st.Action == "goto" && !st.Login {
				check = subst(st.URL, vars)
				break
			}
		}
	}
	if len(o.Cookies) > 0 && !o.ForceLogin {
		if err := page.SetCookies(ctx, o.Cookies); err != nil {
			return fail("saklanan oturum yüklenemedi")
		}
	}
	page.resetDoc()
	if err := page.Navigate(ctx, check); err != nil {
		return fail(err.Error())
	}
	page.WaitIdle(ctx, time.Now(), 500*time.Millisecond, o.StepTimeout)
	needs, err := page.needsLogin(ctx, a)
	if err != nil {
		return fail("sayfa denetlenemedi")
	}
	switch {
	case !needs:
		res.Session = "reused"
		r.Name = "Oturum geçerli (giriş atlandı)"
	default:
		res.Session = "login"
		r.Name = "Giriş yapıldı"
		if a.Mode == "form" {
			if err := page.formLogin(ctx, a, vars, o.StepTimeout); err != nil {
				res.LoginFailed = true
				return fail(err.Error())
			}
		} else {
			for _, st := range steps {
				if !st.Login {
					continue
				}
				if err := doStep(ctx, page, st, vars, o); err != nil {
					res.LoginFailed = true
					return fail(err.Error())
				}
				page.WaitIdle(ctx, time.Now(), 400*time.Millisecond, o.StepTimeout)
			}
		}
		page.WaitIdle(ctx, time.Now(), 500*time.Millisecond, o.StepTimeout)
		if still, err := page.needsLogin(ctx, a); err != nil || still {
			res.LoginFailed = true
			return fail("giriş başarısız (kullanıcı adı/parola hatalı olabilir veya giriş tamamlanmadı)")
		}
	}
	r.OK, r.MS, r.Page = true, float64(time.Since(t0).Microseconds())/1000, shortURL(page.URL())
	if o.Shots {
		if sh, e := page.Shot(ctx); e == nil {
			r.Shot = sh
		}
	}
	res.Steps = append(res.Steps, r)
	return nil
}
