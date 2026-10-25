package pgapi

import (
	"context"
	"net/http"

	"github.com/meme-radar/radar/internal/api"
)

func (b *Backend) checkedToken(ctx context.Context, chain api.Chain, address string, needDecimals bool) ([]byte, *int16, error) {
	token, err := tokenAddress(chain, address)
	if err != nil {
		return nil, nil, err
	}
	decimals, found, err := b.st.APITokenPrecision(ctx, string(chain), token)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, api.Errorf(http.StatusNotFound, "not_found", "", "token not found")
	}
	if needDecimals && decimals == nil {
		return nil, nil, api.Errorf(http.StatusServiceUnavailable, "api_unavailable", "", "token decimals unknown")
	}
	return token, decimals, nil
}
