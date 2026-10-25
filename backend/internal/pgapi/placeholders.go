package pgapi

import (
	"context"
)

// ActiveListVersion reads H3's current active membership snapshot.
func (b *Backend) ActiveListVersion(ctx context.Context) (string, error) {
	return b.activeListVersion(ctx)
}
