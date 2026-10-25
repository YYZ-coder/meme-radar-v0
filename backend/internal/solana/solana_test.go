package solana_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/solana"
)

// sample is a real mainnet transaction from testdata/pump_txs.json.zst,
// recorded 2026-10-23 from getBlock/getTransaction (base64 encoding).
type sample struct {
	Name      string
	Slot      uint64
	Index     int
	BlockTime int64
	Tx        json.RawMessage
}

func samples(t *testing.T) map[string]sample {
	t.Helper()
	z, err := os.ReadFile("testdata/pump_txs.json.zst")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := zstd.NewReader(nil)
	defer d.Close()
	b, err := d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list []sample
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	m := map[string]sample{}
	for _, s := range list {
		m[s.Name] = s
	}
	return m
}

func TestParseTxResolvesAllWireFormats(t *testing.T) {
	all := samples(t)
	// Expectations: an independent Python parser of the same bytes; the
	// signatures were confirmed with getTransaction on mainnet.
	for _, tc := range []struct {
		name, version, sig, payer string
		signers, keys, ixs        int
		failed                    bool
	}{
		{"pump.Trade", "0", "4KZayLcbUefiMKhj8tfGYHpjJY7wBKe81yioF5eMokeyEC7uX6tY7d4AAstMBB39uYRZQ8u8ZevbeQkkqiH5JFqF", "Gygj9QQby4j2jryqyqBHvLP7ctv2SaANgh4sCb69BUpA", 1, 22, 8, false},
		{"pump.Create", "1", "4SajkmZyaxSqAEqJqgpUV7tkdzuPFQBm1Z2JLdou8faDqzhgEcybuPbaZ6SvxSGa8ioFj8qGogYc6ecDQPufnyZ1", "722tXm5uB5uuG2tC43uKHXQ7Pbyz75pTokEAJv9x5VTx", 2, 40, 45, false},
		{"pump.Complete", "legacy", "2MZNFkLNzAnDcZwxDLJZWyszDg78FtU2SaPJj3ahhw26YfJA7tZjLMRqn5FPbnga9umdtxxQD1X1mc8DdE8agmh1", "FrwtUoo8TcadkJfrVbCwdNMa6MFkjhtVJrxCQprMFNeq", 1, 25, 23, false},
		{"amm.Buy", "1", "SpwW2NJRmRNZfrJiqasUGYawydNb8KXFtTQKadfdvUNtSfCeHzACzTijghNuuWrvz9nMJwu7gJFFdrxz7Ddnx3K", "DUKyEHvovWqXGATUHji7cP9kyuNP8KzzsCfz78cSqWGM", 1, 36, 32, false},
		{"failed", "legacy", "3HTCgN2FyeKTs2nYCH87tnwi1aTDpAeRtUhW1QCuv5Cb8XrX2japKUfCVcEQUWdozUABqvSZvM4QNKjQ253YvkTZ", "Cf4YaJKZg3R3GKZPomy5AMGvCMHwewxoDddtJmmNdoWN", 1, 19, 4, true},
	} {
		s := all[tc.name]
		tx, err := solana.ParseTx(s.Index, s.Tx)
		if err != nil {
			t.Fatalf("%s (v%s): %v", tc.name, tc.version, err)
		}
		if got := solana.EncodeBase58(tx.Signature[:]); got != tc.sig {
			t.Errorf("%s signature = %s; want %s", tc.name, got, tc.sig)
		}
		if len(tx.Signers) != tc.signers || solana.EncodeBase58(tx.Signers[0][:]) != tc.payer {
			t.Errorf("%s signers = %d (payer %s); want %d (payer %s)", tc.name, len(tx.Signers), solana.EncodeBase58(tx.Signers[0][:]), tc.signers, tc.payer)
		}
		if len(tx.Keys) != tc.keys || len(tx.Instructions) != tc.ixs || tx.Failed != tc.failed || tx.Index != s.Index {
			t.Errorf("%s keys %d ixs %d failed %v index %d; want %d %d %v %d", tc.name, len(tx.Keys), len(tx.Instructions), tx.Failed, tx.Index,
				tc.keys, tc.ixs, tc.failed, s.Index)
		}
	}
}

func TestEventsFindsAnchorCPIEventsAndEmitters(t *testing.T) {
	all := samples(t)
	pump := solana.MustKey("6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P")
	amm := solana.MustKey("pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA")
	// Expectations from the independent Python prototype: event
	// discriminator, emitter index, emitter instruction discriminator.
	for _, tc := range []struct {
		name    string
		program solana.Key
		want    string
	}{
		{"pump.Trade", pump, "[bddb7fd34ee661ee@3:33e685a4017f83ad]"},
		{"pump.Create", pump, "[1b72a94ddeeb6376@0:d6904cec5f8b31b4 bddb7fd34ee661ee@37:e1f7501ed5b38488]"},
		{"pump.Complete", pump, "[bddb7fd34ee661ee@13:66063d1201daebea 5f72619cd42e9808@13:66063d1201daebea]"},
		{"pump.Migration", pump, "[bde95db95c94ea94@0:bbcb121fceedfe29]"},
		{"pump.Migration", amm, "[b1310cd2a076a774@15:e992d18ecf6840bc ae7c4af90451f611@29:8ce9215e845ac28f]"},
		{"amm.Buy", amm, "[67f4521f2cf57777@23:c2ab1c46684d5b2f]"},
		{"amm.Deposit", amm, "[78f83d531f8e6b90@10:f223c68952e1f2b6]"},
		{"unrelated", pump, "[]"},
		{"failed", pump, "[]"},
	} {
		s := all[tc.name]
		tx, err := solana.ParseTx(s.Index, s.Tx)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var got []string
		for _, e := range tx.Events(tc.program) {
			got = append(got, fmt.Sprintf("%x@%d:%x", e.Data[:8], e.Emitter, tx.Instructions[e.Emitter].Data[:8]))
		}
		if fmt.Sprint(got) != tc.want && !(len(got) == 0 && tc.want == "[]") {
			t.Errorf("%s events = %v; want %s", tc.name, got, tc.want)
		}
	}
}

func TestFindProgramAddressDerivesBondingCurve(t *testing.T) {
	// The bonding_curve account (index 3) of the recorded Pump.fun sell in
	// the pump.Trade sample, for mint v3b6…pump.
	mint := solana.MustKey("v3b6fKyRpvpzf6oZ2YehmvMC5cA55cnbhmd6em1pump")
	pump := solana.MustKey("6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P")
	got, _, err := solana.FindProgramAddress([][]byte{[]byte("bonding-curve"), mint[:]}, pump)
	if err != nil || got.String() != "CWQ3mboh7inFnPqTcyVBehvMRQ4qoAy9gyYdFW3nPag" {
		t.Fatalf("FindProgramAddress = %s, %v; want CWQ3mboh7inFnPqTcyVBehvMRQ4qoAy9gyYdFW3nPag", got, err)
	}
}
