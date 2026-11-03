//go:build linux

package probe

import (
	"net"
	"os"
	"syscall"
)

// icmpConn önce ayrıcalıksız ICMP datagram soketini dener (container'da kök gerekmez; net.ipv4.ping_group_range izin vermeli),
// olmazsa ham soketi (CAP_NET_RAW) dener. Dönen bool, datagram soketi olup olmadığını söyler.
func icmpConn() (net.PacketConn, bool, error) {
	if fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP); err == nil {
		f := os.NewFile(uintptr(fd), "icmp")
		pc, e := net.FilePacketConn(f)
		f.Close()
		if e == nil {
			return pc, true, nil
		}
	}
	pc, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	return pc, false, err
}
