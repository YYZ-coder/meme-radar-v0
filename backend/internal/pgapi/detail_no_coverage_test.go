package pgapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgTokenDetailWithoutCoverage(t *testing.T) {
	f := detailFixture(t, false, true)
	var got api.TokenDetail
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", 200, &got)
	if got.AsOf != "2026-10-23T10:00:00.000Z" || got.HolderCoverage.Status != "unknown" || got.HolderCoverage.AsOf != got.AsOf || len(got.Windows) != 4 || got.Windows[0].Volume == nil || *got.Windows[0].Volume != "350" || got.Volume24h == nil || *got.Volume24h != "400" {
		t.Errorf("no coverage must retain observed values at request minute: %+v", got)
	}
}
