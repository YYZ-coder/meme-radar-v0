package pgapi_test

import (
	"net/url"
	"slices"
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgHoldersSmartMoneyFilter(t *testing.T) {
	f := holdingsFixture(t)
	smartLabelMembers(t, f)
	h := f.handler()
	cursor := ""
	// Only the active, non-excluded dev and A survive the filter. Filtering must
	// precede LIMIT; a stable keyset returns both owners once, with total=2.
	for page, address := range []string{"0x00000000000000000000000000000000000000ee", "0x0000000000000000000000000000000000000011"} {
		var got api.Holders
		getPG(t, h, holdersRoute, holdingsToken+"/holders?label=smart_money&limit=1"+cursor, 200, &got)
		if got.Total == nil || *got.Total != 2 {
			t.Errorf("filtered total=%v; want 2", got.Total)
		}
		if len(got.Items) != 1 {
			t.Fatalf("page %d=%+v; want one member", page, got)
		}
		row := got.Items[0]
		if row.Wallet.Address != address || row.Rank != int64(page+1) || !slices.Contains(row.Wallet.Labels, "smart_money") {
			t.Errorf("page %d row=%+v;want %s", page, row, address)
		}
		if page == 0 {
			if got.NextCursor == nil {
				t.Fatal("missing next cursor")
			}
			cursor = "&cursor=" + url.QueryEscape(*got.NextCursor)
		} else if got.NextCursor != nil {
			t.Errorf("final cursor=%v", got.NextCursor)
		}
	}
}
