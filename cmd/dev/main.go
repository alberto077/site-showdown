// Local dev server: go run ./cmd/dev
// Serves public/ with clean URLs and mounts the same handlers Vercel runs.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"showdown/app"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sites", app.Sites)
	mux.HandleFunc("/api/vote", app.Vote)
	mux.HandleFunc("/api/board", app.Board)
	mux.HandleFunc("/api/admin", app.Admin)

	pub := http.Dir("public")
	fs := http.FileServer(pub)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimSuffix(r.URL.Path, "/")
		if p != "" && filepath.Ext(p) == "" {
			if _, err := os.Stat(filepath.Join("public", p+".html")); err == nil {
				r.URL.Path = p + ".html"
			}
		}
		fs.ServeHTTP(w, r)
	})

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	log.Println("Site Showdown on http://localhost" + addr + "  (host pin: see HOST_PIN, default kaboom)")
	log.Fatal(http.ListenAndServe(addr, mux))
}
