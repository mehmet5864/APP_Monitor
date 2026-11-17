//go:build !linux && !windows

package main

func readProcs() []procSample       { return nil }
func listenPorts() map[int][]int    { return nil }
func listServices() []appInfo       { return nil }
func autoLogDefs() []autoLogDef     { return nil }
func pickLogPath(_ []string) string { return "" }
