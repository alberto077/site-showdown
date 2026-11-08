package handler

import (
	"net/http"

	"showdown/app"
)

// Handler is the Vercel entry point for /api/admin.
func Handler(w http.ResponseWriter, r *http.Request) { app.Admin(w, r) }
