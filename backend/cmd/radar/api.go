package main

import (
	"context"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

func openAPIBackend(ctx context.Context, c apiConfig) (httpapi.Backend, func(), error) {
	if c.store == "mock" {
		return mockdata.New(nil), func() {}, nil
	}
	st, err := store.Open(ctx, c.dbURL)
	if err != nil {
		return nil, nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	backend := pgapi.New(st, c.cfg, nil)
	if c.readCacheMaxAge == 0 {
		return backend, st.Close, nil
	}
	cached, err := pgapi.NewCached(backend, c.readCacheMaxAge)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	if err := cached.PrimeHealth(ctx); err != nil {
		cached.Close()
		st.Close()
		return nil, nil, err
	}
	return cached, func() { cached.Close(); st.Close() }, nil
}
