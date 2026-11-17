package app

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Yalnızca bu adlardaki ajan ikili dosyaları sunulur (yol gezintisini engeller).
var agentFileRe = regexp.MustCompile(`^appmon-agent-(linux|windows)-(amd64|arm64)(\.exe)?$`)

type agentFile struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// GET /api/agent/files: sunucuda hazır duran ajan ikili dosyaları.
func (s *Server) agentFiles(w http.ResponseWriter, r *http.Request, a Auth) {
	out := []agentFile{}
	if s.AgentDir != "" {
		ents, _ := os.ReadDir(s.AgentDir)
		for _, e := range ents {
			m := agentFileRe.FindStringSubmatch(e.Name())
			if m == nil || e.IsDir() {
				continue
			}
			f, err := os.Open(filepath.Join(s.AgentDir, e.Name()))
			if err != nil {
				continue
			}
			h := sha256.New()
			n, _ := io.Copy(h, f)
			f.Close()
			out = append(out, agentFile{Name: e.Name(), OS: m[1], Arch: m[2], Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, 200, map[string]any{"enabled": s.AgentDir != "", "files": out})
}

// GET /agent/dl/{name}: ajan ikili dosyası (kimlik doğrulaması gerekmez; dosya gizli değildir, anahtar içermez).
func (s *Server) agentDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.AgentDir == "" || !agentFileRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, filepath.Join(s.AgentDir, name))
}
