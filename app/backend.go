// Package app owns the store, startup data and shutdown for the web server.
package app

import (
	"context"
	"sync"

	"poople/store"
)

// Backend owns storage. Stop and join HTTP users before Close; cancelling a
// request never closes the shared database.
type Backend struct {
	db       *store.Store
	close    sync.Once
	closeErr error
}

// Open opens the store and prepares every piece of state the server needs
// before it accepts requests; startup loaders run here, after the store opens.
func Open(ctx context.Context, dataDir string) (*Backend, error) {
	db, err := store.Open(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	return &Backend{db: db}, nil
}

// Database is borrowed for the backend lifetime; only Backend closes it.
func (b *Backend) Database() *store.Store { return b.db }

// Close releases storage once; later calls return the first result.
func (b *Backend) Close() error {
	b.close.Do(func() { b.closeErr = b.db.Close() })
	return b.closeErr
}
