//go:build !linux && !windows

package main

func listenSockets() []listenSock    { return nil }
func procInfoFor(pid int) procDetail { return procDetail{} }
