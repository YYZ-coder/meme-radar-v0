package pgapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

func decodePosition(cursor string) (*store.APIPosition, error) {
	if cursor == "" {
		return nil, nil
	}
	bad := api.Errorf(http.StatusBadRequest, "invalid_param", "cursor", "malformed cursor")
	if len(cursor) > 1024 {
		return nil, bad
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return nil, bad
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil || len(keys) != 4 {
		return nil, bad
	}
	for _, name := range []string{"block_time", "block_num", "tx_index", "ix_index"} {
		if v, ok := keys[name]; !ok || bytes.Equal(v, []byte("null")) {
			return nil, bad
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p store.APIPosition
	if dec.Decode(&p) != nil || dec.Decode(new(any)) != io.EOF || p.Time.IsZero() || p.Block < 0 || p.Tx < 0 || p.Ix < 0 {
		return nil, bad
	}
	return &p, nil
}

func encodePosition(p store.APIPosition) (*string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	cursor := base64.RawURLEncoding.EncodeToString(data)
	return &cursor, nil
}

func positionID(p store.APIPosition) string { return fmt.Sprintf("%d:%d:%d", p.Block, p.Tx, p.Ix) }

func walletRef(chain api.Chain, address []byte) *api.WalletRef {
	if len(address) == 0 {
		return nil
	}
	return &api.WalletRef{Address: displayAddress(string(chain), address), Labels: []string{}}
}

func (b *Backend) recentCoverage(ctx context.Context, chain api.Chain) (string, error) {
	asOf, ok, err := b.st.ChainAsOf(ctx, string(chain), b.now())
	if err != nil {
		return "", err
	}
	if !ok {
		return "partial", nil
	}
	complete, err := b.st.Coverage(ctx, string(chain), asOf.Add(-24*time.Hour), asOf)
	if err != nil {
		return "", err
	}
	if complete {
		return "complete", nil
	}
	return "partial", nil
}

// LiquidityEvents maps original events and paginates by exact chain position.
func (b *Backend) LiquidityEvents(ctx context.Context, chain api.Chain, address, cursor string, limit int) (api.LiquidityEvents, error) {
	token, _, err := b.checkedToken(ctx, chain, address, true)
	if err != nil {
		return api.LiquidityEvents{}, err
	}
	after, err := decodePosition(cursor)
	if err != nil {
		return api.LiquidityEvents{}, err
	}
	rows, found, err := b.st.APILiquidityEvents(ctx, string(chain), token, after, limit+1)
	if err != nil {
		return api.LiquidityEvents{}, err
	}
	if !found {
		return api.LiquidityEvents{}, api.Errorf(http.StatusNotFound, "not_found", "", "token not found")
	}
	coverage, err := b.recentCoverage(ctx, chain)
	if err != nil {
		return api.LiquidityEvents{}, err
	}
	out := api.LiquidityEvents{Coverage: coverage, Items: []api.LiquidityEvent{}}
	if len(rows) > limit {
		out.NextCursor, err = encodePosition(rows[limit-1].Position)
		if err != nil {
			return api.LiquidityEvents{}, err
		}
		rows = rows[:limit]
	}
	for _, r := range rows {
		if r.TokenAmount == nil {
			return api.LiquidityEvents{}, unavailable()
		}
		out.Items = append(out.Items, api.LiquidityEvent{ID: positionID(r.Position), Time: r.Position.Time.Format("2006-01-02T15:04:05.000Z"), Kind: r.Kind,
			TokenAmount: *r.TokenAmount, QuoteAmount: r.QuoteAmount, QuoteSymbol: r.QuoteSymbol, ValueUSD: r.ValueUSD, Wallet: walletRef(chain, r.Wallet),
			TxHash: displayAddress(string(chain), r.TxHash), Pool: displayAddress(string(chain), r.Pool), Dex: r.Dex})
	}
	return out, nil
}
