package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Yerel OTLP alıcısı (agent 1.2.0): uygulamalardaki OpenTelemetry SDK'ları trace'leri bu makinedeki ajana
// (varsayılan 127.0.0.1:4318) gönderir; ajan bunları kendi anahtarıyla APP_MON'a iletir. Uygulamaya
// sunucu adresi veya anahtar vermek gerekmez; sunucuya ulaşılamazsa bellekte (sınırlı) tamponlanır.
// Metrik (yalnızca JSON) iletilir, günlük (logs) sessizce atılır.

const (
	otlpMaxBody  = 16 << 20
	otlpMaxQueue = 32 << 20
)

var otlpBackoffMin, otlpBackoffMax = 2 * time.Second, 60 * time.Second

type otlpItem struct {
	path, ctype, enc string
	body             []byte
}

type otlpProxy struct {
	base, key string
	client    *http.Client
	mu        sync.Mutex
	q         []otlpItem
	size      int
	wake      chan struct{}
	dropped   int
	sent      int
}

func newOTLPProxy(serverBase, key string, client *http.Client) *otlpProxy {
	return &otlpProxy{base: strings.TrimRight(serverBase, "/"), key: key, client: client, wake: make(chan struct{}, 1)}
}

func (p *otlpProxy) enqueue(it otlpItem) {
	p.mu.Lock()
	p.q = append(p.q, it)
	p.size += len(it.body)
	for p.size > otlpMaxQueue && len(p.q) > 1 { // en eskiyi at
		p.size -= len(p.q[0].body)
		p.q = p.q[1:]
		p.dropped++
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *otlpProxy) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("appmon-agent otlp " + Version))
	})
	mux.HandleFunc("POST /v1/{kind}", func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		ct := r.Header.Get("Content-Type")
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, otlpMaxBody))
		if err != nil {
			http.Error(w, "gövde okunamadı veya çok büyük", http.StatusRequestEntityTooLarge)
			return
		}
		proto := strings.Contains(ct, "protobuf")
		switch {
		case kind == "traces", kind == "metrics" && !proto:
			p.enqueue(otlpItem{path: "/v1/" + kind, ctype: ct, enc: r.Header.Get("Content-Encoding"), body: body})
		case kind == "metrics" || kind == "logs": // sunucu desteklemiyor: SDK hata üretmesin diye sessizce kabul et
		default:
			http.NotFound(w, r)
			return
		}
		if proto {
			w.Header().Set("Content-Type", "application/x-protobuf")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
	})
	return mux
}

// post tek öğeyi sunucuya gönderir: (tamam, yeniden denenebilir).
func (p *otlpProxy) post(it otlpItem) (bool, bool) {
	req, err := http.NewRequest(http.MethodPost, p.base+it.path, bytes.NewReader(it.body))
	if err != nil {
		return false, false
	}
	if it.ctype != "" {
		req.Header.Set("Content-Type", it.ctype)
	}
	if it.enc != "" {
		req.Header.Set("Content-Encoding", it.enc)
	}
	req.Header.Set("X-API-Key", p.key)
	resp, err := p.client.Do(req)
	if err != nil {
		return false, true
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode < 300:
		return true, false
	case resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500:
		return false, true
	}
	log.Printf("otlp: sunucu %s için HTTP %d döndürdü; veri atıldı (anahtar rolü, lisans veya gövde biçimi?)", it.path, resp.StatusCode)
	return false, false
}

// run kuyruğu boşaltır; sunucu hata verirse üstel geri çekilmeyle yeniden dener.
func (p *otlpProxy) run(ctx context.Context) {
	backoff := otlpBackoffMin
	for {
		p.mu.Lock()
		var it otlpItem
		have := len(p.q) > 0
		if have {
			it = p.q[0]
		}
		p.mu.Unlock()
		if !have {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			}
			continue
		}
		ok, retry := p.post(it)
		if !ok && retry {
			log.Printf("otlp: sunucuya iletilemedi, %s sonra yeniden denenecek (%d öğe tamponda)", backoff, p.queued())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > otlpBackoffMax {
				backoff = otlpBackoffMax
			}
			continue
		}
		backoff = otlpBackoffMin
		p.mu.Lock()
		if len(p.q) > 0 { // başı çıkar
			p.size -= len(p.q[0].body)
			p.q = p.q[1:]
		}
		if ok {
			p.sent++
		}
		p.mu.Unlock()
	}
}

func (p *otlpProxy) queued() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.q)
}

// startOTLP yerel alıcıyı başlatır; adres kullanımdaysa ajan çalışmaya devam eder.
func startOTLP(listen string, p *otlpProxy) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Printf("otlp: %s dinlenemedi (%v); yerel trace alıcısı kapalı", listen, err)
		return
	}
	if host, _, _ := net.SplitHostPort(listen); !isLoopback(host) && host != "localhost" {
		log.Printf("otlp: UYARI %s loopback değil; ağdaki herkes bu makine adına trace gönderebilir", listen)
	}
	log.Printf("otlp: yerel trace alıcısı http://%s/v1/traces", listen)
	go p.run(context.Background())
	srv := &http.Server{Handler: p.handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("otlp: %v", err)
		}
	}()
}
