package handler

import (
	"net/http"

	"showdown/app"
)

// Handler is the Vercel entry point for /api/vote.
func Handler(w http.ResponseWriter, r *http.Request) { app.Vote(w, r) }
