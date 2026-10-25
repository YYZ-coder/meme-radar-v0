// Read-only H6 online verification: no Store, migrations or enrich role.
// Run from backend: go run testdata/expect/probe_metadata.go
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/metadata"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		panic(err)
	}
	f := metadata.NewFetcher(metadata.Options{IPFSGateways: cfg.Metadata.IPFSGateways, RPS: cfg.Metadata.RPS, PerHostRPS: cfg.Metadata.PerHostRPS, FourMemeAPIURL: cfg.Metadata.FourMemeAPIURL, FourMemeRPS: cfg.Metadata.FourMemeRPS})
	defer f.CloseIdleConnections()
	raw, err := os.ReadFile("internal/metadata/testdata/pump_sources.json")
	if err != nil {
		panic(err)
	}
	var manifest []map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		panic(err)
	}
	ipfsOnly := len(os.Args) > 1 && os.Args[1] == "--ipfs-only"
	output := map[string]any{"recorded_at": time.Now().UTC().Format(time.RFC3339), "mode": "read-only Fetcher/ChainSource; no Store or enrich role"}
	var rows []map[string]any
	for _, source := range manifest {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		uri := source["original_uri"]
		if ipfsOnly {
			uri = "ipfs://" + source["cid"]
		}
		doc, err := f.Fetch(ctx, uri)
		cancel()
		row := map[string]any{"mint": source["mint"], "source_uri": uri}
		if err != nil {
			row["error"] = err.Error()
		} else {
			digest := sha256.Sum256(doc.Raw)
			row["status"] = "ok"
			row["image_url"] = doc.ImageURL
			row["socials"] = doc.Socials
			row["raw_sha256_matches_cid"] = hex.EncodeToString(digest[:]) == source["sha256"]
		}
		rows = append(rows, row)
	}
	output["pump"] = rows
	if !ipfsOnly {
		address, _ := hex.DecodeString("5d709925a8671dbd97d73d0b1f3053ec66dfffff")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		doc, err := f.FetchFourMeme(ctx, address)
		cancel()
		if err != nil {
			output["fourmeme_error"] = err.Error()
		} else {
			output["fourmeme"] = map[string]any{"symbol": doc.Symbol, "name": doc.Name, "image_url": doc.ImageURL, "socials": doc.Socials, "description": doc.Description}
		}
		ch, _ := cfg.Chain("base")
		rpc := jsonrpc.New(ch.Ingest.RPCURL, &http.Client{Timeout: 10 * time.Second}, nil, nil)
		doppler, _ := hex.DecodeString("2b6fffed44e7d32acdea9306bfe738fe5cff838e")
		uri, err := metadata.NewChainSource("evm", rpc).URI(context.Background(), doppler)
		if err != nil {
			output["doppler_error"] = err.Error()
		} else {
			output["doppler_token_uri"] = uri
		}
	}
	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}
