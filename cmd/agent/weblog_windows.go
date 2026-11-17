//go:build windows

package main

import (
	"os"
	"path/filepath"
)

func webCandidates() []webCand {
	sd := os.Getenv("SystemDrive")
	if sd == "" {
		sd = "C:"
	}
	return []webCand{{server: "iis", perDir: true, globs: []string{filepath.Join(sd+`\`, "inetpub", "logs", "LogFiles", "W3SVC*", "u_ex*.log")}}}
}
