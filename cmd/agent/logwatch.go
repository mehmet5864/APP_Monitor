package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
)

// logWatch bir günlük dosyasını takip edip yeni satırlarda deseni sayar (tail -F benzeri; dönmeyi/kısalmayı algılar).
type logWatch struct {
	name, path string
	re         *regexp.Regexp
	f          *os.File
	off        int64
	started    bool
	tail       string // henüz satır sonu gelmemiş kalan parça
	// Otomatik uygulama günlüğü (v2.7.3): ikinci desen uyarı satırlarını sayar, son hata satırı maskelenip örnek tutulur.
	warnRe   *regexp.Regexp
	sampleOn bool
	lastWarn int    // son poll'da sayılan uyarı satırı
	sample   string // son eşleşen (hata) satırı, maskeli
	readable bool
}

var logNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// parseLogSpecs "ad::dosya::regex" girdilerini ";;" ile ayrılmış liste olarak çözer.
func parseLogSpecs(spec string) ([]*logWatch, error) {
	var out []*logWatch
	seen := map[string]bool{}
	for _, e := range strings.Split(spec, ";;") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		p := strings.SplitN(e, "::", 3)
		if len(p) != 3 {
			return nil, fmt.Errorf("günlük tanımı ad::dosya::regex biçiminde olmalı: %q", e)
		}
		name, path, pat := strings.TrimSpace(p[0]), strings.TrimSpace(p[1]), p[2]
		if !logNameRe.MatchString(name) {
			return nil, fmt.Errorf("günlük adı yalnızca harf, rakam, _ ve - içerebilir (en çok 40): %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("günlük adı tekrar ediyor: %q", name)
		}
		seen[name] = true
		if path == "" || pat == "" {
			return nil, fmt.Errorf("dosya ve regex boş olamaz: %q", e)
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("regex geçersiz (%s): %v", name, err)
		}
		out = append(out, &logWatch{name: name, path: path, re: re})
	}
	if len(out) > 20 {
		return nil, fmt.Errorf("en çok 20 günlük izlenebilir")
	}
	return out, nil
}

const maxLogRead = 4 << 20 // bir turda en çok 4 MB okunur

// poll yeni satırları okur; (eşleşen, toplam satır, okunabilir) döndürür. İlk çağrıda dosya sonuna gidilir (geçmiş sayılmaz).
func (w *logWatch) poll() (matches, lines int, ok bool) {
	w.lastWarn = 0
	matches, lines, ok = w.poll1()
	w.readable = ok
	return
}

func (w *logWatch) poll1() (matches, lines int, ok bool) {
	fi, err := os.Stat(w.path)
	if err != nil {
		w.close()
		return 0, 0, false
	}
	if w.f != nil {
		if of, err := w.f.Stat(); err != nil || !os.SameFile(fi, of) || fi.Size() < w.off {
			w.close() // döndürüldü veya kısaltıldı: baştan oku
			w.started = true
		}
	}
	if w.f == nil {
		f, err := os.Open(w.path)
		if err != nil {
			return 0, 0, false
		}
		w.f, w.tail = f, ""
		if !w.started {
			w.off = fi.Size()
			w.started = true
		} else {
			w.off = 0
		}
	}
	if _, err := w.f.Seek(w.off, io.SeekStart); err != nil {
		w.close()
		return 0, 0, false
	}
	buf, err := io.ReadAll(io.LimitReader(w.f, maxLogRead))
	if err != nil {
		w.close()
		return 0, 0, false
	}
	w.off += int64(len(buf))
	data := w.tail + string(buf)
	i := strings.LastIndexByte(data, '\n')
	if i < 0 {
		w.tail = data
		if len(w.tail) > 1<<20 {
			w.tail = ""
		}
		return 0, 0, true
	}
	w.tail = data[i+1:]
	for _, ln := range strings.Split(data[:i], "\n") {
		lines++
		ln = strings.TrimRight(ln, "\r")
		if w.re.MatchString(ln) {
			matches++
			if w.sampleOn {
				w.sample = maskLine(ln)
			}
		} else if w.warnRe != nil && w.warnRe.MatchString(ln) {
			w.lastWarn++
		}
	}
	return matches, lines, true
}

func (w *logWatch) close() {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
}

// logMetrics her izlenen günlük için log.<ad>.count (eşleşen satır), log.<ad>.lines ve log.<ad>.readable üretir.
func logMetrics(ws []*logWatch) []metric {
	var ms []metric
	for _, w := range ws {
		m, l, ok := w.poll()
		r := 0.0
		if ok {
			r = 1
			ms = append(ms, metric{Name: "log." + w.name + ".count", Value: float64(m)}, metric{Name: "log." + w.name + ".lines", Value: float64(l)})
		}
		ms = append(ms, metric{Name: "log." + w.name + ".readable", Value: r})
	}
	return ms
}

func initLogs(spec string) []*logWatch {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	ws, err := parseLogSpecs(spec)
	if err != nil {
		log.Fatalf("günlük izleme ayarı hatalı: %v", err)
	}
	for _, w := range ws {
		w.poll() // başlangıç konumunu dosya sonuna al
	}
	return ws
}
