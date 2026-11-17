package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// -web-setup / -web-undo (yalnızca Linux, root): nginx ve Apache'ye istek süresini de içeren ikinci bir erişim günlüğü ekler.
// Ayar dosyası tek ve ajana aittir (nginx: conf.d/appmon-access.conf, Apache: conf.d veya conf-available/appmon-access.conf);
// mevcut dosyalar değiştirilmez. Yeniden yüklemeden önce ayar sınanır; hata olursa dosya silinir.

const webRiskText = `RİSKLER — lütfen okuyun:
 1. Üretimdeki web sunucusunun ayarına yeni bir dosya eklenir ve ayar yeniden yüklenir (reload; bağlantılar kesilmez). Yeniden yüklemeden önce
    ayar sınanır, hata çıkarsa dosya silinip hiçbir şey değişmeden bırakılır. Yine de önce üretim dışı bir makinede deneyin.
 2. Sanal sunucunun (server/VirtualHost) kendi access_log / CustomLog satırı varsa o sunucu otomatik kapsanmaz; ajan bu sunucuları listeler,
    hedef satırı oralara sizin eklemeniz gerekir. Hiçbir mevcut ayar dosyası değiştirilmez.
 3. Her istek için ikinci bir günlük satırı yazılır (ek disk ve G/Ç). Dosya: /var/log/appmon/access.log; günlük döndürülür, 3 gün tutulur.
 4. Günlükte tam istek adresi (sorgu dizesi dahil) yerelde saklanır; sunucuya yalnızca sorgusuz, normalleştirilmiş (/siparis/{id}) özet gider.
 5. SELinux açıksa günlük dizinine httpd_log_t etiketi verilir.
 Geri alma: sudo appmon-agent -web-undo`

const nginxFmt = `log_format appmon_fmt 'appmon h=$host m=$request_method st=$status rt=$request_time uri=$request_uri';`
const apacheFmt = `LogFormat "appmon h=%v m=%m st=%>s rt_us=%D uri=%U" appmon_fmt`
const nginxLine = `access_log /var/log/appmon/access.log appmon_fmt;`
const apacheLine = `CustomLog /var/log/appmon/access.log appmon_fmt`

const webConfHeader = "# APP_MON ajanı tarafından yazıldı (appmon-agent -web-setup). Kaldırmak için: appmon-agent -web-undo\n"

func nginxConf() string  { return webConfHeader + nginxFmt + "\n" + nginxLine + "\n" }
func apacheConf() string { return webConfHeader + apacheFmt + "\n" + apacheLine + "\n" }

var apacheOverrideRe = regexp.MustCompile(`(?i)^\s*(CustomLog|TransferLog)\s+`)
var vhostOpenRe = regexp.MustCompile(`(?i)^\s*<VirtualHost\b`)
var vhostCloseRe = regexp.MustCompile(`(?i)^\s*</VirtualHost>`)
var nginxLogRe = regexp.MustCompile(`^access_log\s+(\S+)`)

// scanOverrides sanal sunucu düzeyinde kendi erişim günlüğünü tanımlayan satırları ("dosya:satır: içerik") bulur; bunlar
// ajanın http/ana düzeydeki ek günlüğünü devralmaz. nginx'te ayar dosyasındaki http düzeyi (derinlik 1) satırlar sayılmaz
// (aynı düzeyde birden çok access_log yazılabilir); Apache'de yalnızca <VirtualHost> içindeki CustomLog/TransferLog sayılır.
// ownFile (ajanın kendi dosyası), ajanın kendi satırı ve "access_log off" atlanır.
func scanOverrides(kind string, files []string, ownFile string) []string {
	var out []string
	for _, f := range files {
		if f == ownFile {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		depth := 1 // conf.d dosyaları http bağlamında içeri alınır
		if filepath.Base(f) == "nginx.conf" {
			depth = 0
		}
		inVhost := false
		for i, ln := range strings.Split(string(b), "\n") {
			if c := strings.Index(ln, "#"); c >= 0 && kind == "nginx" {
				ln = ln[:c]
			}
			t := strings.TrimSpace(ln)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			hit := false
			if kind == "apache" {
				switch {
				case vhostOpenRe.MatchString(ln):
					inVhost = true
				case vhostCloseRe.MatchString(ln):
					inVhost = false
				case inVhost && apacheOverrideRe.MatchString(ln) && !strings.Contains(t, "/var/log/appmon/"):
					hit = true
				}
			} else {
				// Deyimleri { } ; ile bölüp derinliği izle.
				for _, seg := range splitNginx(t) {
					switch seg {
					case "{":
						depth++
					case "}":
						depth--
					default:
						if m := nginxLogRe.FindStringSubmatch(seg); m != nil && depth >= 2 && m[1] != "off" && !strings.Contains(seg, "/var/log/appmon/") {
							hit = true
						}
					}
				}
			}
			if hit {
				out = append(out, fmt.Sprintf("%s:%d: %s", f, i+1, t))
				if len(out) >= 30 {
					return out
				}
			}
		}
	}
	return out
}

// splitNginx bir satırı "{", "}" ve ";" ile biten deyim parçalarına böler.
func splitNginx(line string) []string {
	var out []string
	cur := strings.Builder{}
	flush := func() {
		if v := strings.TrimSpace(cur.String()); v != "" {
			out = append(out, v)
		}
		cur.Reset()
	}
	for _, c := range line {
		switch c {
		case '{':
			flush()
			out = append(out, "{")
		case '}':
			flush()
			out = append(out, "}")
		case ';':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}
