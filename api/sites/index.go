package handler

import (
	"net/http"

	"showdown/app"
)

// Handler is the Vercel entry point for /api/sites.
func Handler(w http.ResponseWriter, r *http.Request) { app.Sites(w, r) }
