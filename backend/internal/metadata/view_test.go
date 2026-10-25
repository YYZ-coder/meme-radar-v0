package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/parse"
)

func TestTokenInfoExposesMetadata(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	var columns []string
	if err := db.QueryRow(ctx, `SELECT array_agg(column_name::text ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='token_info'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	// Frozen view order: all 0010 columns, then exactly image_url and socials.
	want := []string{"chain", "address", "symbol", "name", "decimals", "total_supply", "creator", "launch_platform", "launch_at", "created_block", "metadata_uri", "metadata_status", "first_seen_at", "updated_at", "supply", "supply_decimals", "supply_source", "image_url", "socials"}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("token_info columns=%v; want %v", columns, want)
	}
	address, unknown := bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 20)
	six, two := int16(6), int16(2)
	metadataFacts(t, st, "base", 1, now.Add(-time.Minute), []parse.Token{{Address: address, Created: true, Name: "Chain name", Symbol: "CHAIN", TotalSupply: "1000", Decimals: &six, URI: "https://metadata.example/token"}, {Address: unknown}}, true)
	supplied := "999"
	if err := st.SaveSupplement(ctx, "base", address, &supplied, &two, "", now); err != nil {
		t.Fatal(err)
	}
	fallback := "2000"
	if err := st.SaveSupplement(ctx, "base", unknown, &fallback, &two, "", now); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := db.QueryRow(ctx, `SELECT to_jsonb(ti)-'image_url'-'socials' FROM token_info ti WHERE chain='base' AND address=$1`, address).Scan(&before); err != nil {
		t.Fatal(err)
	}
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"image":"https://images.example/a.png","twitter":"@actual","website":"https://project.example"}`))
	})
	if err := New(st, "base", f, nil, nil).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	var image string
	var socials map[string]string
	var same bool
	if err := db.QueryRow(ctx, `SELECT ti.image_url,ti.socials,(ti.image_url=m.image_url AND ti.socials=m.socials),to_jsonb(ti)-'image_url'-'socials' FROM token_info ti JOIN token_metadata m USING(chain,address) WHERE ti.chain='base' AND ti.address=$1`, address).Scan(&image, &socials, &same, &after); err != nil {
		t.Fatal(err)
	}
	if image != "https://images.example/a.png" || !reflect.DeepEqual(socials, map[string]string{"twitter": "https://x.com/actual", "website": "https://project.example"}) || !same {
		t.Errorf("view metadata image=%q socials=%v equal=%v", image, socials, same)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("pre-existing view values changed: before=%s after=%s", before, after)
	}
	var supply, source string
	var dec int16
	if err := db.QueryRow(ctx, `SELECT supply::text,supply_decimals,supply_source FROM token_info WHERE chain='base' AND address=$1`, address).Scan(&supply, &dec, &source); err != nil {
		t.Fatal(err)
	}
	if supply != "1000" || dec != 6 || source != "creation_event" {
		t.Errorf("creation-event precedence changed: %s %d %s", supply, dec, source)
	}
	if err := db.QueryRow(ctx, `SELECT supply::text,supply_decimals,supply_source FROM token_info WHERE chain='base' AND address=$1`, unknown).Scan(&supply, &dec, &source); err != nil {
		t.Fatal(err)
	}
	if supply != "2000" || dec != 2 || source != "rpc" {
		t.Errorf("supplement fallback changed: %s %d %s", supply, dec, source)
	}
	// No metadata source means both exposed fields remain NULL.
	var unknownImage *string
	var unknownSocials []byte
	if err := db.QueryRow(ctx, `SELECT image_url,socials FROM token_info WHERE chain='base' AND address=$1`, unknown).Scan(&unknownImage, &unknownSocials); err != nil {
		t.Fatal(err)
	}
	if unknownImage != nil || unknownSocials != nil {
		t.Errorf("unknown metadata image=%v socials=%s; want NULLs", unknownImage, unknownSocials)
	}
	// H2's PG API is not on this baseline. Validate its shared component schemas
	// with real PG display values here; endpoint regression remains a merge check.
	var tokenRef []byte
	if err := db.QueryRow(ctx, `SELECT jsonb_build_object('chain',chain,'address',addr(chain,address),'symbol',symbol,'name',name,'image_url',image_url) FROM token_info WHERE chain='base' AND address=$1`, address).Scan(&tokenRef); err != nil {
		t.Fatal(err)
	}
	var ref any
	if err := json.Unmarshal(tokenRef, &ref); err != nil {
		t.Fatal(err)
	}
	if err := contracttest.Spec(t).Components.Schemas["TokenRef"].Value.VisitJSON(ref); err != nil {
		t.Errorf("PG metadata TokenRef contract: %v", err)
	}
	normalizedSocials := map[string]any{}
	for key, value := range socials {
		normalizedSocials[key] = value
	}
	if err := contracttest.Spec(t).Components.Schemas["Socials"].Value.VisitJSON(normalizedSocials); err != nil {
		t.Errorf("PG metadata Socials contract: %v", err)
	}
}
