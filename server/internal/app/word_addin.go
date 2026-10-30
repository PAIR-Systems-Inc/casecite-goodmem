package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// The manifest is generated from the configured public origin, never the Host
// header. No separate web server or per-deployment frontend rebuild is needed.
func (s *Server) addinManifest(static fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source, err := fs.ReadFile(static, "word-addin/manifest.template.xml")
		if err != nil {
			http.Error(w, "Word add-in assets are not built", 503)
			return
		}
		var escaped bytes.Buffer
		_ = xml.EscapeText(&escaped, []byte(s.cfg.PublicURL))
		origin := escaped.String()
		xmlText := strings.ReplaceAll(string(source), "https://localhost:3200/", origin+"/word-addin/")
		xmlText = strings.ReplaceAll(xmlText, "https://localhost:3200", origin)
		xmlText = strings.ReplaceAll(xmlText, "https://github.com/thomasunise/casecite", "https://github.com/PAIR-Systems-Inc/casecite-goodmem")
		id := sha256.Sum256([]byte("casecite-goodmem-word:" + s.cfg.PublicURL))
		id[6] = (id[6] & 15) | 128
		id[8] = (id[8] & 63) | 128
		guid := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:16])
		xmlText = strings.ReplaceAll(xmlText, "7c1a0a6e-4d8b-4f3e-9b1a-2c5d6e7f8a90", guid)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="casecite-word.xml"`)
		_, _ = w.Write([]byte(xmlText))
	}
}
