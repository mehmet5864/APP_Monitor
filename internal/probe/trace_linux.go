//go:build linux

package probe

import (
	"errors"
	"net"
	"syscall"
)

func traceConn() (net.PacketConn, error) { return net.ListenPacket("ip4:icmp", "0.0.0.0") }

func setTTL(pc net.PacketConn, ttl int) error {
	ic, ok := pc.(*net.IPConn)
	if !ok {
		return errors.New("beklenmeyen soket türü")
	}
	rc, err := ic.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, ttl) }); err != nil {
		return err
	}
	return serr
}

// setTOS giden paketlerin IP ToS baytını (DSCP<<2) ayarlar.
func setTOS(pc net.PacketConn, tos int) error {
	ic, ok := pc.(*net.IPConn)
	if !ok {
		return errors.New("beklenmeyen soket türü")
	}
	rc, err := ic.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TOS, tos) }); err != nil {
		return err
	}
	return serr
}
