package cdp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Path          string // boşsa APM_CHROME_PATH, sonra PATH araması
	NoSandbox     bool
	Width, Height int
	Lang          string
}

// Browser çalışan bir Chromium süreci ve tek sayfasıdır.
type Browser struct {
	Page *Client
	cmd  *exec.Cmd
	dir  string
	once sync.Once
}

func FindChrome(path string) (string, error) {
	if path == "" {
		path = os.Getenv("APM_CHROME_PATH")
	}
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("Chromium bulunamadı: %s", path)
		}
		return path, nil
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", errors.New("Chromium bulunamadı (APM_CHROME_PATH ayarlayın veya imajda chromium kurulu olsun)")
}

var listenRe = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// Launch başsız (headless) bir Chromium başlatır ve sayfasına bağlanır. Close çağrılmalıdır.
func Launch(ctx context.Context, o Options) (*Browser, error) {
	path, err := FindChrome(o.Path)
	if err != nil {
		return nil, err
	}
	if o.Width == 0 {
		o.Width, o.Height = 1280, 720
	}
	if o.Lang == "" {
		o.Lang = "tr-TR"
	}
	dir, err := os.MkdirTemp("", "appmon-chrome-")
	if err != nil {
		return nil, err
	}
	args := []string{"--headless=new", "--remote-debugging-port=0", "--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check", "--disable-gpu", "--disable-dev-shm-usage", "--mute-audio",
		"--hide-scrollbars", "--disable-extensions", "--disable-background-networking", "--disable-crash-reporter",
		"--disable-sync", "--disable-translate", "--lang=" + o.Lang, fmt.Sprintf("--window-size=%d,%d", o.Width, o.Height), "about:blank"}
	if o.NoSandbox || os.Geteuid() == 0 || os.Getenv("APM_CHROME_NO_SANDBOX") == "true" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	b := &Browser{cmd: cmd, dir: dir}
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		sent := false
		for sc.Scan() { // okumaya devam et ki süreç stderr dolunca takılmasın
			if m := listenRe.FindStringSubmatch(sc.Text()); m != nil && !sent {
				sent = true
				found <- m[1]
			}
		}
	}()
	var browserWS string
	select {
	case browserWS = <-found:
	case <-time.After(20 * time.Second):
		b.Close()
		return nil, errors.New("Chromium zamanında başlamadı")
	case <-ctx.Done():
		b.Close()
		return nil, ctx.Err()
	}
	u, _ := url.Parse(browserWS)
	pageWS, err := firstPageWS(ctx, u.Host)
	if err != nil {
		b.Close()
		return nil, err
	}
	b.Page, err = NewClient(ctx, pageWS)
	if err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func firstPageWS(ctx context.Context, host string) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+host+"/json/list", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			var list []struct {
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(resp.Body).Decode(&list)
			resp.Body.Close()
			for _, t := range list {
				if t.Type == "page" && strings.HasPrefix(t.WS, "ws://") {
					return t.WS, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return "", errors.New("Chromium sayfası bulunamadı")
}

func (b *Browser) Close() {
	b.once.Do(func() {
		if b.Page != nil {
			b.Page.Close()
		}
		if b.cmd.Process != nil {
			b.cmd.Process.Kill()
			b.cmd.Wait()
		}
		os.RemoveAll(b.dir)
	})
}
