package mockdata

import (
	"strings"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

var adminStart = float64(time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC).UnixMilli())

func seedMembers() []api.Member {
	r := rng(hash("members"))
	names := []string{"Alpha Hunter", "Sol Sniper", "Meme Explorer", "Degen Research", "pixelwhale"}
	out := make([]api.Member, 36)
	for i := range out {
		chain := []api.Chain{api.Solana, api.Base, api.BSC}[i%3]
		imported := i < 12
		addr := evmAddress("sm"+num(float64(i)), "")
		if chain == api.Solana {
			addr = solanaAddress("sm"+num(float64(i)), "")
		}
		m := api.Member{Chain: chain, Address: addr, Tags: []string{}, AddedAt: iso(adminStart - float64(i)*3_600_000)}
		if imported && i < len(names) {
			m.Label = ptr(names[i])
			m.Twitter = ptr("@" + strings.ReplaceAll(names[i], " ", ""))
		}
		if imported && r() < 0.5 {
			m.Tags = []string{"kol"}
		}
		switch {
		case imported && i%4 == 0:
			m.Sources = []string{"import", "auto"}
		case imported:
			m.Sources = []string{"import"}
		default:
			m.Sources = []string{"auto"}
		}
		out[i] = m
	}
	return out
}

func memberID(chain api.Chain, address string) string {
	if chain != api.Solana {
		address = strings.ToLower(address)
	}
	return string(chain) + ":" + address
}
