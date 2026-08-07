// Package api serves Kaku's read-only Content API.
package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/db"
)

type Handler struct{ q *db.Queries }

func New(q *db.Queries) *Handler { return &Handler{q: q} }

func (h *Handler) Router() chi.Router { return chi.NewRouter() }
