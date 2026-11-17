package main

import (
	"context"
	"os/exec"
	"time"
)

// readSockets "netstat -ano -p tcp" çıktısından ESTABLISHED yuvaları ve sahibi süreç kimliğini okur.
func readSockets() []sock {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil
	}
	return parseNetstat(string(b))
}
