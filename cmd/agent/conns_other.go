//go:build !linux && !windows

package main

func readSockets() []sock { return nil }
