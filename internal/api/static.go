package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// MountUI serves a built web UI (ui/dist) from dir as static files with
// SPA fallback: any GET that isn't under /api/ and doesn't match a real
// file gets index.html, so client-side routing survives a page refresh.
//
// If dir doesn't exist, this is a no-op (logged once) rather than an
// error — the daemon must keep working API-only for anyone who hasn't
// built the UI, including every existing Go-only CI job that never runs
// npm.
func (s *Server) MountUI(dir string) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		log.Printf("api: UI dist dir %s not found, skipping static UI mount (API-only mode)", dir)
		return
	}

	fileServer := http.FileServer(http.Dir(dir))
	indexPath := filepath.Join(dir, "index.html")

	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := filepath.Clean(r.URL.Path)
		if _, err := os.Stat(filepath.Join(dir, cleaned)); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, indexPath)
	}))

	log.Printf("api: serving UI from %s", dir)
}
