//go:build linux

package main

const ownWebLog = "/var/log/appmon/access.log" // -web-setup'ın yazdığı günlük

func webCandidates() []webCand {
	return []webCand{
		{server: "appmon", globs: []string{ownWebLog}},
		{server: "nginx", globs: []string{"/var/log/nginx/access.log", "/var/log/nginx/*access*.log"}},
		{server: "apache", globs: []string{"/var/log/httpd/access_log", "/var/log/httpd/*access_log", "/var/log/httpd/*access.log",
			"/var/log/apache2/access.log", "/var/log/apache2/*access*.log"}},
	}
}
