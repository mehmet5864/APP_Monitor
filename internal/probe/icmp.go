package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// icmpProbe hedefe Count (varsayılan 3) echo isteği gönderir; en az bir yanıt gelirse başarılıdır.
// Gecikme ortalama RTT, ek metrik packet_loss_pct'tir.
func icmpProbe(ctx context.Context, spec Spec, block bool) (time.Duration, map[string]float64, error) {
	host := spec.Target
	ip, err := resolveIP(ctx, host, block)
	if err != nil {
		return 0, nil, err
	}
	if ip.To4() == nil {
		return 0, nil, errors.New("ICMP şimdilik yalnızca IPv4 hedefleri destekler")
	}
	pc, dgram, err := icmpConn()
	if err != nil {
		return 0, nil, fmt.Errorf("ICMP soketi açılamadı (%v): konteynerde net.ipv4.ping_group_range veya NET_RAW yetkisi gerekir", err)
	}
	defer pc.Close()
	var dst net.Addr = &net.IPAddr{IP: ip}
	if dgram {
		dst = &net.UDPAddr{IP: ip}
	}
	count := spec.Opts.Count
	if count <= 0 || count > 10 {
		count = 3
	}
	id := uint16(os.Getpid() & 0xffff)
	deadline, hasDL := ctx.Deadline()
	var total time.Duration
	got := 0
	buf := make([]byte, 1500)
	for seq := 1; seq <= count; seq++ {
		if ctx.Err() != nil {
			break
		}
		msg := make([]byte, 8+32)
		msg[0] = 8
		binary.BigEndian.PutUint16(msg[4:], id)
		binary.BigEndian.PutUint16(msg[6:], uint16(seq))
		copy(msg[8:], "appmon-icmp-probe-payload-000000")
		binary.BigEndian.PutUint16(msg[2:], icmpChecksum(msg))
		sent := time.Now()
		if _, err := pc.WriteTo(msg, dst); err != nil {
			return 0, nil, err
		}
		wait := sent.Add(time.Second)
		if hasDL && deadline.Before(wait) {
			wait = deadline
		}
		pc.SetReadDeadline(wait)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				break // bu paket için zaman aşımı
			}
			if n >= 8 && buf[0] == 0 && binary.BigEndian.Uint16(buf[6:]) == uint16(seq) &&
				(dgram || binary.BigEndian.Uint16(buf[4:]) == id) {
				total += time.Since(sent)
				got++
				break
			}
		}
		if seq < count {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-ctx.Done():
			}
		}
	}
	loss := float64(count-got) / float64(count) * 100
	if got == 0 {
		return 0, map[string]float64{"packet_loss_pct": 100}, fmt.Errorf("ICMP yanıtı yok (%d/%d paket kayıp)", count, count)
	}
	return total / time.Duration(got), map[string]float64{"packet_loss_pct": loss}, nil
}
