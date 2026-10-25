package store

import "context"

// Ping checks that the database is reachable without modifying data.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
