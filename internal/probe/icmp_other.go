//go:build !linux

package probe

import "net"

func icmpConn() (net.PacketConn, bool, error) {
	pc, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	return pc, false, err
}
