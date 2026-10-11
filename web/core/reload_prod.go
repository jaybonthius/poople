//go:build !dev

package core

import (
	"context"

	"github.com/go-chi/chi/v5"
)

// Development is true only in `task live` builds (-tags dev).
const Development = false

func reloadRoutes(context.Context, chi.Router) {}
