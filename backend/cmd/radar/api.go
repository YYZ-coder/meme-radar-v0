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
	return pgapi.New(st, c.cfg, nil), st.Close, nil
}
