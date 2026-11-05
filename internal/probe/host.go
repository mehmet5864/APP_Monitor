package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Host Resources MIB (RFC 2790) ve UCD-SNMP OID'leri.
const (
	oidHrProcLoad = "1.3.6.1.2.1.25.3.3.1.2"
	oidHrSType    = "1.3.6.1.2.1.25.2.3.1.2"
	oidHrSDescr   = "1.3.6.1.2.1.25.2.3.1.3"
	oidHrSSize    = "1.3.6.1.2.1.25.2.3.1.5"
	oidHrSUsed    = "1.3.6.1.2.1.25.2.3.1.6"
	oidSsCpuIdle  = "1.3.6.1.4.1.2021.11.11.0"

	hrStorageOther     = "1.3.6.1.2.1.25.2.1.1"
	hrStorageRam       = "1.3.6.1.2.1.25.2.1.2"
	hrStorageFixedDisk = "1.3.6.1.2.1.25.2.1.4"
)

func snmpProbeV3(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	s, closer, err := dialSNMP(ctx, d, spec.Target, block, spec.Opts.SNMPAuth())
	if err != nil {
		return nil, err
	}
	defer closer()
	oid := spec.Opts.OID
	if oid == "" {
		oid = defaultOID
	}
	v, err := s.Get(oid)
	if err != nil {
		return nil, err
	}
	if e := spec.Opts.Expect; e != "" && !strings.Contains(v.Text(), e) {
		return nil, fmt.Errorf("SNMP değeri %q beklenen %q içermiyor", truncate(v.Text(), 60), e)
	}
	if f, ok := v.Num(); ok {
		return map[string]float64{"snmp_value": f}, nil
	}
	return nil, nil
}

var diskNameRe = regexp.MustCompile(`[^A-Za-z0-9_./\\:-]`)

func diskName(descr string) string {
	f := strings.Fields(descr)
	n := descr
	if len(f) > 0 {
		n = f[0]
	}
	n = diskNameRe.ReplaceAllString(n, "_")
	if len(n) > 40 {
		n = n[:40]
	}
	return n
}

func want(o Options, m string) bool {
	if len(o.Metrics) == 0 {
		return true
	}
	for _, x := range o.Metrics {
		if x == m {
			return true
		}
	}
	return false
}

func hostProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	if spec.Opts.Method == "ssh" {
		return sshHostProbe(ctx, d, spec, block)
	}
	s, closer, err := dialSNMP(ctx, d, spec.Target, block, spec.Opts.SNMPAuth())
	if err != nil {
		return nil, err
	}
	defer closer()
	return collectSNMPHost(ctx, s, spec.Opts)
}

// collectSNMPHost CPU, RAM ve disk doluluk yüzdelerini toplar: host.cpu_pct, host.mem_pct, host.disk_pct (en dolu disk)
// ve her disk için host.disk_pct.<ad>.
func collectSNMPHost(ctx context.Context, s *snmpSession, o Options) (map[string]float64, error) {
	out := map[string]float64{}
	// Bağlantı/kimlik denetimi: sysUpTime
	up, err := s.Get(defaultOID)
	if err != nil {
		return nil, err
	}
	if f, ok := up.Num(); ok {
		out["host.uptime_sec"] = f / 100
	}
	if want(o, "cpu") {
		vals, err := s.Walk(ctx, oidHrProcLoad, 256)
		if err != nil {
			return nil, err
		}
		var sum, n float64
		for _, v := range vals {
			if f, ok := v.Num(); ok {
				sum += f
				n++
			}
		}
		if n > 0 {
			out["host.cpu_pct"] = sum / n
		} else if v, err := s.Get(oidSsCpuIdle); err == nil {
			if f, ok := v.Num(); ok {
				out["host.cpu_pct"] = 100 - f
			}
		}
	}
	if want(o, "mem") || want(o, "disk") {
		type row struct {
			typ, descr string
			size, used float64
		}
		rows := map[string]*row{} // dizin -> satır
		col := func(base string, set func(r *row, v snmpVal)) error {
			vals, err := s.Walk(ctx, base, 256)
			if err != nil {
				return err
			}
			for _, v := range vals {
				idx := strings.TrimPrefix(v.OID, base+".")
				r := rows[idx]
				if r == nil {
					r = &row{}
					rows[idx] = r
				}
				set(r, v)
			}
			return nil
		}
		if err := col(oidHrSType, func(r *row, v snmpVal) { r.typ = v.Text() }); err != nil {
			return nil, err
		}
		if err := col(oidHrSDescr, func(r *row, v snmpVal) { r.descr = v.Text() }); err != nil {
			return nil, err
		}
		if err := col(oidHrSSize, func(r *row, v snmpVal) { r.size, _ = v.Num() }); err != nil {
			return nil, err
		}
		if err := col(oidHrSUsed, func(r *row, v snmpVal) { r.used, _ = v.Num() }); err != nil {
			return nil, err
		}
		var ramSize, ramUsed, bufCache float64
		var maxDisk float64 = -1
		for _, r := range rows {
			switch {
			case r.typ == hrStorageRam && r.size > 0:
				ramSize, ramUsed = r.size, r.used
			case r.typ == hrStorageOther && r.size > 0:
				// Linux net-snmp: tampon ve önbellek ayrı satırlardır; kullanılan bellekten düşülür.
				dl := strings.ToLower(r.descr)
				if strings.Contains(dl, "cached memory") || strings.Contains(dl, "memory buffers") {
					bufCache += r.used
				}
			case r.typ == hrStorageFixedDisk && r.size > 0 && want(o, "disk"):
				pct := 100 * r.used / r.size
				out["host.disk_pct."+diskName(r.descr)] = pct
				if pct > maxDisk {
					maxDisk = pct
				}
			}
		}
		if want(o, "mem") && ramSize > 0 {
			u := ramUsed - bufCache
			if u < 0 {
				u = ramUsed
			}
			out["host.mem_pct"] = 100 * u / ramSize
		}
		if maxDisk >= 0 {
			out["host.disk_pct"] = maxDisk
		}
	}
	if len(out) <= 1 {
		return nil, errors.New("hiçbir ölçüm alınamadı (cihaz Host Resources MIB desteklemiyor veya SNMP görünümü kısıtlı olabilir)")
	}
	return out, nil
}
