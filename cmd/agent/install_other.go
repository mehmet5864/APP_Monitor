//go:build !linux && !windows

package main

import "fmt"

func defaultConfig() string { return "" }
func sendConfFile() string  { return "" }
func doInstall(settings) error {
	return fmt.Errorf("bu işletim sisteminde otomatik kurulum yok; ajanı elle çalıştırın")
}
func doUninstall() error { return fmt.Errorf("bu işletim sisteminde otomatik kurulum yok") }
