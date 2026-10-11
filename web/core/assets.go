package core

import (
	"context"

	"poople/lib/static"

	"github.com/go-chi/chi/v5"
)

func Routes(ctx context.Context, router chi.Router) {
	router.Handle("/core/static/*", static.Handler("/core/static", StaticDirectory, "core"))
	reloadRoutes(ctx, router)
}
