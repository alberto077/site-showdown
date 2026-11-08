package handler

import (
	"net/http"

	"showdown/app"
)

// Handler is the Vercel entry point for /api/board.
func Handler(w http.ResponseWriter, r *http.Request) { app.Board(w, r) }
