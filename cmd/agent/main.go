// APP_MON agent: sunucuya periyodik olarak ana makine metriklerini gönderen, yalnızca standart kütüphane kullanan tek ikili dosya.
// Tercih edilen yöntem agent'sız izlemedir (probe ve OTLP); agent yalnızca CPU, bellek ve disk gibi ana makine
// metrikleri gerektiğinde kurulur.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func main() {
	host, _ := os.Hostname()
	server := flag.String("server", env("APM_SERVER", "http://localhost:8080"), "APP_MON sunucu adresi")
	key := flag.String("key", env("APM_KEY", ""), "agent API anahtarı")
	name := flag.String("name", env("APM_NAME", host), "asset adı")
	interval := flag.Duration("interval", 15*time.Second, "gönderim aralığı")
	flag.Parse()
	if *key == "" {
		log.Fatal("APM_KEY (veya -key) zorunlu")
	}
	endpoint := strings.TrimRight(*server, "/") + "/api/ingest"
	client := &http.Client{Timeout: 10 * time.Second}
	c := newCollector()

	log.Printf("agent başladı: %s -> %s (her %s)", *name, endpoint, *interval)
	for ; ; time.Sleep(*interval) {
		body, _ := json.Marshal(map[string]any{
			"asset":   map[string]any{"name": *name, "kind": "host", "labels": map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}},
			"metrics": c.collect(),
		})
		if err := send(client, endpoint, *key, body); err != nil {
			log.Printf("gönderim başarısız: %v", err)
		}
	}
}

func send(client *http.Client, endpoint, key string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
