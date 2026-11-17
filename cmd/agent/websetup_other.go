//go:build !linux

package main

import "fmt"

func webSetup(dry, yes bool) error {
	return fmt.Errorf("-web-setup yalnızca Linux'ta kullanılır (IIS günlükleri ayar gerektirmeden otomatik okunur)")
}

func webUndo() error { return fmt.Errorf("-web-undo yalnızca Linux'ta kullanılır") }
