//go:build !linux

package probe

import (
	"errors"
	"net"
)

func traceConn() (net.PacketConn, error) {
	return nil, errors.New("yol izleme yalnızca Linux sunucuda desteklenir")
}
func setTTL(net.PacketConn, int) error { return errors.New("desteklenmiyor") }
func setTOS(net.PacketConn, int) error { return errors.New("desteklenmiyor") }
