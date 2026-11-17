package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOTLPProxy(t *testing.T) {
	otlpBackoffMin, otlpBackoffMax = 20*time.Millisecond, 50*time.Millisecond
	var calls, fails atomic.Int32
	got := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-API-Key") != "k1" {
			w.WriteHeader(401)
			return
		}
		if fails.Load() > 0 { // sunucu geçici hata: yeniden denenmeli
			fails.Add(-1)
			w.WriteHeader(503)
			return
		}
		b := new(bytes.Buffer)
		b.ReadFrom(r.Body)
		got <- r.URL.Path + "|" + r.Header.Get("Content-Type") + "|" + r.Header.Get("Content-Encoding") + "|" + b.String()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	p := newOTLPProxy(srv.URL, "k1", srv.Client())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.run(ctx)
	local := httptest.NewServer(p.handler())
	defer local.Close()
	post := func(path, ct, enc, body string) (int, string) {
		req, _ := http.NewRequest("POST", local.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", ct)
		if enc != "" {
			req.Header.Set("Content-Encoding", enc)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b := new(bytes.Buffer)
		b.ReadFrom(resp.Body)
		return resp.StatusCode, b.String()
	}
	// JSON trace: olduğu gibi (başlıklar ve gövde) iletilir; SDK'ya hemen {} döner
	if c, b := post("/v1/traces", "application/json", "", `{"resourceSpans":[]}`); c != 200 || b != "{}" {
		t.Fatalf("yanıt: %d %q", c, b)
	}
	select {
	case m := <-got:
		if m != `/v1/traces|application/json||{"resourceSpans":[]}` {
			t.Fatalf("iletilen: %s", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("trace iletilmedi")
	}
	// sunucu 503 verirse tamponlanır ve yeniden denenir; gzip başlığı korunur
	fails.Store(2)
	if c, _ := post("/v1/traces", "application/x-protobuf", "gzip", "PB-BYTES"); c != 200 {
		t.Fatalf("protobuf kabul: %d", c)
	}
	select {
	case m := <-got:
		if m != "/v1/traces|application/x-protobuf|gzip|PB-BYTES" {
			t.Fatalf("yeniden denenen: %s", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("yeniden deneme sonrası iletilmedi")
	}
	if calls.Load() < 4 {
		t.Fatalf("503 sonrası yeniden denenmeli: %d çağrı", calls.Load())
	}
	// günlük ve protobuf metrik sessizce atılır (sunucuya gitmez)
	before := calls.Load()
	if c, _ := post("/v1/logs", "application/json", "", "{}"); c != 200 {
		t.Fatalf("logs: %d", c)
	}
	if c, _ := post("/v1/metrics", "application/x-protobuf", "", "x"); c != 200 {
		t.Fatalf("metrics pb: %d", c)
	}
	if c, _ := post("/v1/bilinmeyen", "application/json", "", "{}"); c != 404 {
		t.Fatalf("bilinmeyen yol 404 olmalı: %d", c)
	}
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != before {
		t.Fatal("logs/protobuf-metrics sunucuya gitmemeli")
	}
	// yanlış anahtar (401) kalıcı hata: veri atılır, kuyruk takılmaz
	bad := newOTLPProxy(srv.URL, "yanlis", srv.Client())
	go bad.run(ctx)
	bad.enqueue(otlpItem{path: "/v1/traces", ctype: "application/json", body: []byte("{}")})
	deadline := time.Now().Add(3 * time.Second)
	for bad.queued() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if bad.queued() != 0 {
		t.Fatal("401 sonrası öğe atılmalı")
	}
	// kuyruk sınırı: en eski öğeler atılır
	q := newOTLPProxy(srv.URL, "k1", srv.Client())
	big := make([]byte, 20<<20)
	q.enqueue(otlpItem{path: "/v1/traces", body: big})
	q.enqueue(otlpItem{path: "/v1/traces", body: big})
	if q.queued() != 1 || q.dropped != 1 {
		t.Fatalf("kuyruk sınırı: %d öğe, %d atılan", q.queued(), q.dropped)
	}
}
