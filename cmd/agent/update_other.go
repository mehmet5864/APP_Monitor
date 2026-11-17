//go:build !linux && !windows

package main

import (
	"fmt"
	"net/http"
)

func cleanupOld() {}

func readUpdateErr() {}

func applyUpdate() error { return fmt.Errorf("desteklenmiyor") }

func platformUpdate(client *http.Client, cfg settings, o *updateOffer) (int, error) {
	return 0, fmt.Errorf("bu platformda uzaktan güncelleme yok")
}

func remoteDir() string { return "" }
func canRestart() bool  { return false }
func restartSelf()      {}
