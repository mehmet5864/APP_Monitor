package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMaskAndExtract(t *testing.T) {
	m := maskLog("kullanıcı ali@firma.com giriş yaptı password=Sup3rS3cret token: abcdef0123456789abcdef0123456789abcdef kart 4111111111111111")
	for _, bad := range []string{"ali@firma.com", "Sup3rS3cret", "abcdef0123456789abcdef0123456789abcdef", "4111111111111111"} {
		if strings.Contains(m, bad) {
			t.Errorf("maskelenmedi: %s -> %s", bad, m)
		}
	}
	if got := maskLog(strings.Repeat("a b ", 200)); len([]rune(got)) > logMsgMax+1 {
		t.Errorf("kısaltılmadı: %d", len(got))
	}
	tid := "0af7651916cd43dd8448eb211c80319c"
	if maskLog("istek trace_id="+tid+" hata") == "" || !strings.Contains(maskLog("istek trace_id="+tid+" hata"), tid) {
		t.Error("trace kimliği korunmalı")
	}
	tr, sp := extractTrace("", "", nil, `{"msg":"x","trace_id":"`+tid+`","span_id":"b7ad6b7169203331"}`)
	if tr != tid || sp != "b7ad6b7169203331" {
		t.Errorf("mesajdan çıkarma: %q %q", tr, sp)
	}
	if tr, _ = extractTrace("", "", nil, "traceparent: 00-"+tid+"-b7ad6b7169203331-01"); tr != tid {
		t.Errorf("traceparent: %q", tr)
	}
	if tr, _ = extractTrace("", "", map[string]string{"traceId": strings.ToUpper(tid)}, ""); tr != tid {
		t.Errorf("öznitelik: %q", tr)
	}
	if tr, _ = extractTrace("zzz", "", nil, "yok"); tr != "" {
		t.Errorf("geçersiz kimlik kabul edildi: %q", tr)
	}
}

func TestLogsEndToEnd(t *testing.T) {
	s, _, do := scaleServer(t)
	tid := "0af7651916cd43dd8448eb211c80319c"
	body := map[string]any{"resourceLogs": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "odeme-api"}}}},
		"scopeLogs": []any{map[string]any{"logRecords": []any{
			map[string]any{"timeUnixNano": "1", "severityNumber": 17, "body": map[string]any{"stringValue": "veritabanı zaman aşımı kullanıcı a@b.com"}, "traceId": tid, "spanId": "b7ad6b7169203331"},
			map[string]any{"severityNumber": 9, "body": map[string]any{"stringValue": "bilgi satırı"}},
			map[string]any{"severityText": "WARN", "body": map[string]any{"stringValue": "yavaş sorgu"}},
		}}}}}}

	// Kapalıyken hiçbir şey saklanmaz ama istemci 200 alır.
	c, b, _ := do("POST", "/v1/logs", body)
	if c != 200 || !strings.Contains(string(b), "rejectedLogRecords") {
		t.Fatalf("kapalı: %d %s", c, b)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM log_events`).Scan(&n)
	if n != 0 {
		t.Fatalf("kapalıyken %d satır yazıldı", n)
	}

	s.LogOn = true
	if c, b, _ = do("POST", "/v1/logs", body); c != 200 {
		t.Fatalf("alım: %d %s", c, b)
	}
	s.DB.QueryRow(`SELECT count(*) FROM log_events`).Scan(&n)
	if n != 2 { // bilgi satırı varsayılan uyarı eşiğinin altında
		t.Fatalf("saklanan = %d, beklenen 2", n)
	}
	var msg string
	s.DB.QueryRow(`SELECT message FROM log_events WHERE trace_id = $1`, tid).Scan(&msg)
	if strings.Contains(msg, "a@b.com") || !strings.Contains(msg, "{eposta}") {
		t.Fatalf("e-posta maskelenmedi: %s", msg)
	}
	// asset oluşturulmamalı (lisans etkisi yok)
	s.DB.QueryRow(`SELECT count(*) FROM assets WHERE name = 'odeme-api'`).Scan(&n)
	if n != 0 {
		t.Fatalf("günlük alımı asset oluşturdu")
	}

	// trace + yaklaşık eşleşme
	now := time.Now()
	s.DB.Exec(`INSERT INTO spans (tenant_id, trace_id, span_id, service, name, start_time, dur_us, status) VALUES (1,$1,'b7ad6b7169203331','odeme-api','POST /pay',$2,2000000,2)`, tid, now.Add(-time.Second))
	s.DB.Exec(`UPDATE log_events SET time = $1 WHERE trace_id = ''`, now)
	c, b, _ = do("GET", "/api/traces/"+tid+"/logs", nil)
	var tl struct {
		Exact, Near []logOut
	}
	json.Unmarshal(b, &tl)
	if c != 200 || len(tl.Exact) != 1 || len(tl.Near) != 1 {
		t.Fatalf("trace logları: %d exact=%d near=%d %s", c, len(tl.Exact), len(tl.Near), b)
	}
	if tl.Exact[0].Level != "error" {
		t.Errorf("seviye = %s", tl.Exact[0].Level)
	}

	// basit JSON + liste + süzgeçler
	if c, b, _ = do("POST", "/api/logs", map[string]any{"logs": []any{map[string]any{"service": "web", "level": "error", "message": "hata trace_id=" + tid}, map[string]any{"service": "web", "level": "debug", "message": "atlanır"}}}); c != 200 {
		t.Fatalf("api/logs: %d %s", c, b)
	}
	c, b, _ = do("GET", "/api/logs?trace_id="+tid, nil)
	var lr struct{ Logs []logOut }
	json.Unmarshal(b, &lr)
	if c != 200 || len(lr.Logs) != 2 {
		t.Fatalf("liste: %d %d %s", c, len(lr.Logs), b)
	}
	if c, b, _ = do("GET", "/api/logs?level=error&q=zaman", nil); c != 200 || !bytes.Contains(b, []byte("odeme-api")) {
		t.Fatalf("süzgeç: %d %s", c, b)
	}

	// hız sınırı
	s.LogRate = 1
	s.lograte = nil
	many := []any{}
	for i := 0; i < 5; i++ {
		many = append(many, map[string]any{"service": "x", "level": "error", "message": "m"})
	}
	_, b, _ = do("POST", "/api/logs", map[string]any{"logs": many})
	if !bytes.Contains(b, []byte("rejectedLogRecords")) {
		t.Errorf("hız sınırı çalışmadı: %s", b)
	}

	// saklama temizliği
	s.DB.Exec(`UPDATE log_events SET time = now() - interval '10 days'`)
	s.purgeOld(t.Context())
	s.DB.QueryRow(`SELECT count(*) FROM log_events`).Scan(&n)
	if n != 0 {
		t.Errorf("eski satırlar silinmedi: %d", n)
	}
}

func TestLogsProtobuf(t *testing.T) {
	tid := "0af7651916cd43dd8448eb211c80319c"
	tb := make([]byte, 16)
	for i := range tb {
		tb[i] = byte(i + 1)
	}
	_ = tid
	str := func(f int, v string) []byte { return pbLen(f, []byte(v)) }
	any := pbLen(5, str(1, "hata oluştu"))
	rec := append(append(any, pbVarF(2, 17)...), pbLen(9, tb)...)
	scope := pbLen(2, rec)
	rl := append(pbLen(1, pbLen(1, append(str(1, "service.name"), pbLen(2, str(1, "svc"))...))), pbLen(2, scope)...)
	res, err := parseLogsPB(pbLen(1, rl))
	if err != nil || len(res) != 1 || len(res[0].Logs) != 1 {
		t.Fatalf("pb: %v %+v", err, res)
	}
	lg := res[0].Logs[0]
	if res[0].Attrs["service.name"] != "svc" || lg.Body != "hata oluştu" || lg.Severity != 17 || len(lg.TraceID) != 32 {
		t.Errorf("pb alanları: %+v %v", lg, res[0].Attrs)
	}
}
