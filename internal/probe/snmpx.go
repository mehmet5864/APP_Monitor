package probe

import (
	"context"
	"fmt"
	"math"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CustomOID kullanıcı tanımlı tek bir SNMP ölçümüdür. Metrik adı "snmp.<Name>" olur.
type CustomOID struct {
	Name  string  `json:"name"`
	OID   string  `json:"oid"`
	Scale float64 `json:"scale,omitempty"` // değer bununla çarpılır (0 = 1)
	Rate  bool    `json:"rate,omitempty"`  // sayaç: saniyedeki artışa çevrilir
}

var customNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,60}$`)

// SNMPTemplates snmpx probe'unun hazır şablonları (etiket Arayüz'de gösterilir).
var SNMPTemplates = map[string]string{
	"interfaces": "Arayüz trafiği (ifTable/ifXTable): bps, hata, doluluk, durum",
	"linux":      "Linux net-snmp (UCD): CPU, bellek, takas, yük",
	"cisco":      "Cisco IOS: CPU, bellek, sıcaklık",
	"ups":        "UPS (RFC 1628): pil, yük, kalan süre, besleme kaynağı",
}

// ValidateSNMPX seçenekleri denetler.
func ValidateSNMPX(o Options) error {
	if o.Template != "" {
		if _, ok := SNMPTemplates[o.Template]; !ok {
			return fmt.Errorf("şablon interfaces, linux, cisco veya ups olmalı")
		}
	}
	if len(o.OIDs) > 30 {
		return fmt.Errorf("en çok 30 özel OID tanımlanabilir")
	}
	seen := map[string]bool{}
	for _, c := range o.OIDs {
		if !customNameRe.MatchString(c.Name) {
			return fmt.Errorf("özel OID adı harf, rakam, _ . - içermeli (en çok 60): %q", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("özel OID adı tekrar ediyor: %s", c.Name)
		}
		seen[c.Name] = true
		if !ValidOID(c.OID) {
			return fmt.Errorf("%s: OID noktalı sayılar olmalı", c.Name)
		}
		if c.Scale < 0 || math.IsNaN(c.Scale) || math.IsInf(c.Scale, 0) {
			return fmt.Errorf("%s: ölçek geçersiz", c.Name)
		}
	}
	if o.IfFilter != "" {
		if len(o.IfFilter) > 200 {
			return fmt.Errorf("arayüz filtresi çok uzun")
		}
		if _, err := regexp.Compile(o.IfFilter); err != nil {
			return fmt.Errorf("arayüz filtresi geçerli bir düzenli ifade olmalı: %v", err)
		}
	}
	if o.Template == "" && len(o.OIDs) == 0 {
		return fmt.Errorf("bir şablon seçin veya en az bir özel OID ekleyin")
	}
	return nil
}

// rateStore sayaçların önceki örneklerini tutar (asset yeniden başlasa da sunucu ömrü boyunca).
var rateStore = struct {
	sync.Mutex
	m map[string]rateSample
}{m: map[string]rateSample{}}

type rateSample struct {
	v   float64
	t   time.Time
	t32 bool
}

// rate sayaç farkını saniyeye çevirir; ilk örnekte ok=false döner. 32 bit sayaçlar sarabilir.
func rate(key string, v float64, typ byte, now time.Time) (float64, bool) {
	rateStore.Lock()
	defer rateStore.Unlock()
	prev, had := rateStore.m[key]
	rateStore.m[key] = rateSample{v: v, t: now, t32: typ != 0x46}
	if !had {
		return 0, false
	}
	dt := now.Sub(prev.t).Seconds()
	if dt < 0.5 {
		return 0, false
	}
	d := v - prev.v
	if d < 0 {
		if prev.t32 && prev.v < 4294967296 {
			d += 4294967296 // Counter32 sarması
		} else {
			return 0, false // sıfırlama
		}
	}
	return d / dt, true
}

func sanitizeName(s string) string {
	s = diskNameRe.ReplaceAllString(strings.TrimSpace(s), "_")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "if"
	}
	return s
}

func snmpxProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	s, closer, err := dialSNMP(ctx, d, spec.Target, block, spec.Opts.SNMPAuth())
	if err != nil {
		return nil, err
	}
	defer closer()
	return collectSNMPX(ctx, s, spec.Target, spec.Opts, time.Now())
}

func collectSNMPX(ctx context.Context, s *snmpSession, target string, o Options, now time.Time) (map[string]float64, error) {
	out := map[string]float64{}
	// Bağlantı/kimlik denetimi + çalışma süresi
	up, err := s.Get(defaultOID)
	if err != nil {
		return nil, err
	}
	if f, ok := up.Num(); ok {
		out["snmp.uptime_s"] = f / 100
	}
	switch o.Template {
	case "interfaces":
		if err := collectInterfaces(ctx, s, target, o, now, out); err != nil {
			return nil, err
		}
	case "linux":
		collectLinuxUCD(s, out)
	case "cisco":
		collectCisco(ctx, s, out)
	case "ups":
		collectUPS(s, out)
	}
	for _, c := range o.OIDs {
		v, err := s.Get(c.OID)
		if err != nil {
			if err == errSNMPEnd || strings.Contains(err.Error(), "noSuch") {
				continue // bu cihazda yok: diğerlerini toplamaya devam
			}
			return nil, fmt.Errorf("%s: %v", c.Name, err)
		}
		f, ok := v.Num()
		if !ok {
			continue
		}
		if c.Rate {
			r, ok := rate(target+"|"+c.OID, f, v.Type, now)
			if !ok {
				continue
			}
			f = r
		}
		if c.Scale > 0 {
			f *= c.Scale
		}
		out["snmp."+c.Name] = f
	}
	return out, nil
}

// OID'ler
const (
	oidIfDescr   = "1.3.6.1.2.1.2.2.1.2"
	oidIfOper    = "1.3.6.1.2.1.2.2.1.8"
	oidIfInOct   = "1.3.6.1.2.1.2.2.1.10"
	oidIfOutOct  = "1.3.6.1.2.1.2.2.1.16"
	oidIfInErr   = "1.3.6.1.2.1.2.2.1.14"
	oidIfOutErr  = "1.3.6.1.2.1.2.2.1.20"
	oidIfName    = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCIn    = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOut   = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfHighSpd = "1.3.6.1.2.1.31.1.1.1.15"
	oidIfSpeed   = "1.3.6.1.2.1.2.2.1.5"
)

func collectInterfaces(ctx context.Context, s *snmpSession, target string, o Options, now time.Time, out map[string]float64) error {
	col := func(base string) (map[string]snmpVal, error) {
		vals, err := s.Walk(ctx, base, 512)
		m := map[string]snmpVal{}
		for _, v := range vals {
			m[strings.TrimPrefix(v.OID, base+".")] = v
		}
		return m, err
	}
	descr, err := col(oidIfDescr)
	if err != nil {
		return err
	}
	if len(descr) == 0 {
		return fmt.Errorf("cihaz arayüz tablosu (ifTable) döndürmedi")
	}
	names, _ := col(oidIfName) // ifXTable yoksa ifDescr kullanılır
	oper, _ := col(oidIfOper)
	hcIn, _ := col(oidIfHCIn)
	hcOut, _ := col(oidIfHCOut)
	in32, _ := col(oidIfInOct)
	out32, _ := col(oidIfOutOct)
	inErr, _ := col(oidIfInErr)
	outErr, _ := col(oidIfOutErr)
	high, _ := col(oidIfHighSpd)
	spd, _ := col(oidIfSpeed)
	var re *regexp.Regexp
	if o.IfFilter != "" {
		re, _ = regexp.Compile(o.IfFilter)
	}
	maxIf := 64
	used := map[string]bool{}
	n := 0
	for idx, dv := range descr {
		if n >= maxIf {
			break
		}
		name := dv.Text()
		if nv, ok := names[idx]; ok && nv.Text() != "" {
			name = nv.Text()
		}
		if re != nil && !re.MatchString(name) && !re.MatchString(dv.Text()) {
			continue
		}
		key := sanitizeName(name)
		if used[key] {
			key = key + "_" + idx
		}
		used[key] = true
		n++
		base := "net." + key
		isUp := 0.0
		if ov, ok := oper[idx]; ok {
			if f, _ := ov.Num(); f == 1 {
				isUp = 1
			}
		}
		out[base+".up"] = isUp
		pick := func(hc, lo map[string]snmpVal, dir string) {
			v, ok := hc[idx]
			if !ok || v.Type != 0x46 {
				v, ok = lo[idx]
			}
			if !ok {
				return
			}
			f, isNum := v.Num()
			if !isNum {
				return
			}
			if r, ok := rate(target+"|"+base+"."+dir, f, v.Type, now); ok {
				out[base+"."+dir+"_bps"] = r * 8
			}
		}
		pick(hcIn, in32, "in")
		pick(hcOut, out32, "out")
		for dir, m := range map[string]map[string]snmpVal{"in_err": inErr, "out_err": outErr} {
			if v, ok := m[idx]; ok {
				if f, isNum := v.Num(); isNum {
					if r, ok := rate(target+"|"+base+"."+dir, f, v.Type, now); ok {
						out[base+"."+dir+"_ps"] = r
					}
				}
			}
		}
		// Doluluk: hat hızına göre (Mbps; ifHighSpeed yoksa ifSpeed bps).
		var speed float64
		if hv, ok := high[idx]; ok {
			if f, _ := hv.Num(); f > 0 {
				speed = f * 1e6
			}
		}
		if speed == 0 {
			if sv, ok := spd[idx]; ok {
				if f, _ := sv.Num(); f > 0 && f < 4294967295 {
					speed = f
				}
			}
		}
		if speed > 0 {
			ib, iok := out[base+".in_bps"]
			ob, ook := out[base+".out_bps"]
			if iok || ook {
				out[base+".util_pct"] = math.Min(100, 100*math.Max(ib, ob)/speed)
			}
		}
	}
	return nil
}

func getNum(s *snmpSession, oid string) (float64, bool) {
	v, err := s.Get(oid)
	if err != nil {
		return 0, false
	}
	return v.Num()
}

func collectLinuxUCD(s *snmpSession, out map[string]float64) {
	if idle, ok := getNum(s, oidSsCpuIdle); ok {
		out["host.cpu_pct"] = 100 - idle
	}
	total, ok1 := getNum(s, "1.3.6.1.4.1.2021.4.5.0") // memTotalReal (kB)
	avail, ok2 := getNum(s, "1.3.6.1.4.1.2021.4.6.0") // memAvailReal
	if ok1 && ok2 && total > 0 {
		buf, _ := getNum(s, "1.3.6.1.4.1.2021.4.14.0")
		cache, _ := getNum(s, "1.3.6.1.4.1.2021.4.15.0")
		used := total - avail - buf - cache
		if used < 0 {
			used = total - avail
		}
		out["host.mem_pct"] = 100 * used / total
	}
	st, ok1 := getNum(s, "1.3.6.1.4.1.2021.4.3.0")
	sa, ok2 := getNum(s, "1.3.6.1.4.1.2021.4.4.0")
	if ok1 && ok2 && st > 0 {
		out["host.swap_pct"] = 100 * (st - sa) / st
	}
	for i, n := range []string{"host.load1", "host.load5", "host.load15"} {
		if v, ok := getNum(s, fmt.Sprintf("1.3.6.1.4.1.2021.10.1.5.%d", i+1)); ok {
			out[n] = v / 100 // laLoadInt yüz ile çarpılmış
		}
	}
}

func collectCisco(ctx context.Context, s *snmpSession, out map[string]float64) {
	if vals, err := s.Walk(ctx, "1.3.6.1.4.1.9.9.109.1.1.1.1.6", 64); err == nil && len(vals) > 0 { // cpmCPUTotal5secRev
		var sum float64
		var n int
		for _, v := range vals {
			if f, ok := v.Num(); ok {
				sum += f
				n++
			}
		}
		if n > 0 {
			out["host.cpu_pct"] = sum / float64(n)
		}
	}
	used, ok1 := getNum(s, "1.3.6.1.4.1.9.9.48.1.1.1.5.1") // ciscoMemoryPoolUsed (işlemci havuzu)
	free, ok2 := getNum(s, "1.3.6.1.4.1.9.9.48.1.1.1.6.1")
	if ok1 && ok2 && used+free > 0 {
		out["host.mem_pct"] = 100 * used / (used + free)
	}
	if vals, err := s.Walk(ctx, "1.3.6.1.4.1.9.9.13.1.3.1.3", 64); err == nil && len(vals) > 0 { // sıcaklık (°C)
		mx := -1e9
		for _, v := range vals {
			if f, ok := v.Num(); ok && f > mx {
				mx = f
			}
		}
		if mx > -1e9 {
			out["snmp.temp_max_c"] = mx
		}
	}
}

func collectUPS(s *snmpSession, out map[string]float64) {
	m := map[string]string{
		"snmp.ups_battery_status": "1.3.6.1.2.1.33.1.2.1.0", // 2=normal, 3=düşük, 4=bitmiş
		"snmp.ups_battery_pct":    "1.3.6.1.2.1.33.1.2.4.0",
		"snmp.ups_minutes_left":   "1.3.6.1.2.1.33.1.2.3.0",
		"snmp.ups_output_source":  "1.3.6.1.2.1.33.1.4.1.0", // 3=şebeke, 5=pil
		"snmp.ups_load_pct":       "1.3.6.1.2.1.33.1.4.4.1.5.1",
		"snmp.ups_input_voltage":  "1.3.6.1.2.1.33.1.3.3.1.3.1",
		"snmp.ups_battery_temp_c": "1.3.6.1.2.1.33.1.2.7.0",
	}
	for k, oid := range m {
		if v, ok := getNum(s, oid); ok {
			out[k] = v
		}
	}
	if v, ok := out["snmp.ups_output_source"]; ok {
		if v == 5 {
			out["snmp.ups_on_battery"] = 1
		} else {
			out["snmp.ups_on_battery"] = 0
		}
	}
}
