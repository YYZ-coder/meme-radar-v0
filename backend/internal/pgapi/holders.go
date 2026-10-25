package pgapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"regexp"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

type holderCursor struct {
	Chain    api.Chain               `json:"chain"`
	Token    []byte                  `json:"token"`
	Label    string                  `json:"label"`
	Position store.APIHolderPosition `json:"position"`
	Rank     int64                   `json:"rank"`
}

var atomicBalance = regexp.MustCompile(`^[1-9][0-9]{0,77}$`)

func decodeHolderCursor(cursor string, chain api.Chain, token []byte, label string) (*holderCursor, error) {
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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p holderCursor
	if dec.Decode(&p) != nil || dec.Decode(new(any)) != io.EOF || p.Chain != chain || !bytes.Equal(p.Token, token) || p.Label != label || len(p.Position.Owner) != len(token) || !atomicBalance.MatchString(p.Position.Balance) || p.Rank < 1 || p.Rank > math.MaxInt64-100 {
		return nil, bad
	}
	return &p, nil
}

// Holders ranks observed balances by atomic quantity and owner, with no OFFSET.
// Smart-money labels use the published H3 snapshot, never pending intentions.
func (b *Backend) Holders(ctx context.Context, chain api.Chain, address, label, cursor string, limit int) (api.Holders, error) {
	token, _, err := b.checkedToken(ctx, chain, address, true)
	if err != nil {
		return api.Holders{}, err
	}
	page, err := decodeHolderCursor(cursor, chain, token, label)
	if err != nil {
		return api.Holders{}, err
	}
	var after *store.APIHolderPosition
	var rank int64
	if page != nil {
		after = &page.Position
		rank = page.Rank
	}
	data, err := b.st.APIHolders(ctx, string(chain), token, after, limit+1, label == "smart_money")
	if err != nil {
		return api.Holders{}, err
	}
	out := api.Holders{Coverage: api.Coverage{Status: data.Coverage, Since: optionalTime(data.Since), AsOf: b.now().UTC().Format("2006-01-02T15:04:05.000Z")}, Total: data.Total, Items: []api.Holder{}}
	if data.AsOf != nil {
		out.Coverage.AsOf = *optionalTime(data.AsOf)
	}
	if len(data.Rows) > limit {
		last := data.Rows[limit-1]
		encoded, err := json.Marshal(holderCursor{Chain: chain, Token: token, Label: label, Position: store.APIHolderPosition{Balance: last.Atomic, Owner: last.Owner}, Rank: rank + int64(limit)})
		if err != nil {
			return api.Holders{}, err
		}
		value := base64.RawURLEncoding.EncodeToString(encoded)
		out.NextCursor = &value
		data.Rows = data.Rows[:limit]
	}
	addresses := make([]string, len(data.Rows))
	for i, row := range data.Rows {
		addresses[i] = displayAddress(string(chain), row.Owner)
	}
	native := b.native.page(ctx, chain, addresses, b.now())
	for i, row := range data.Rows {
		wallet := *walletRef(chain, row.Owner)
		if row.Dev {
			wallet.Labels = append(wallet.Labels, "dev")
		}
		if row.SmartMoney {
			wallet.Labels = append(wallet.Labels, "smart_money")
		}
		out.Items = append(out.Items, api.Holder{Rank: rank + int64(i) + 1, Wallet: wallet, Amount: row.Amount, SharePct: row.SharePct, NativeBalance: native[wallet.Address]})
	}
	return out, nil
}
