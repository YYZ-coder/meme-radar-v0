package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetadataWriteRetriesOnlyTheRejectedRawAndIsolatesRows(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	// Verify the real server's SQLSTATEs independently of SaveMetadata.
	for _, tc := range []struct{ raw, code string }{{`{"text":"\u0000"}`, "22P05"}, {`{not JSON}`, "22P02"}} {
		_, err := db.Exec(ctx, `SELECT $1::jsonb`, tc.raw)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != tc.code {
			t.Fatalf("fixture %q SQLSTATE=%v; want %s", tc.raw, err, tc.code)
		}
	}
	// A local test trigger supplies the third explicitly supported character code.
	if _, err := db.Exec(ctx, `CREATE FUNCTION reject_raw_character() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.raw ? 'force_22025' THEN RAISE EXCEPTION 'test invalid escape' USING ERRCODE='22025'; END IF;
 RETURN NEW; END $$; CREATE TRIGGER reject_raw BEFORE INSERT OR UPDATE ON token_metadata FOR EACH ROW EXECUTE FUNCTION reject_raw_character()`); err != nil {
		t.Fatal(err)
	}
	raws := []string{`{"good":1}`, `{"text":"\u0000"}`, `{not JSON}`, `{"force_22025":true}`, `{"good":5}`, `{"good":6}`}
	var writes []store.MetadataWrite
	for i, raw := range raws {
		writes = append(writes, store.MetadataWrite{Address: bytes.Repeat([]byte{byte(i + 1)}, 20), Status: "ok", Attempts: 1, Raw: json.RawMessage(raw), Description: "clean\x00\ntext", ImageURL: "https://images.example/to\x00ken.png", Socials: map[string]string{"twitter": "https://x.com/ac\x00count"}})
	}
	writes[4].Status = "invalid"
	before := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("base", "raw_omitted"))
	err := st.SaveMetadata(ctx, "base", writes, metadataDay)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" {
		t.Errorf("only bad status should remain an error, got %v", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM token_metadata`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("saved rows=%d; want five usable rows across rejected raw and invalid-status row", count)
	}
	for i, write := range writes {
		if i == 4 {
			continue
		}
		var rawNull bool
		var warning *string
		var status, description, image, twitter string
		err := db.QueryRow(ctx, `SELECT raw IS NULL,error,status,description,image_url,socials->>'twitter' FROM token_metadata WHERE chain='base' AND address=$1`, write.Address).Scan(&rawNull, &warning, &status, &description, &image, &twitter)
		if err != nil {
			t.Errorf("row %d not persisted: %v", i, err)
			continue
		}
		omitted := i >= 1 && i <= 3
		if rawNull != omitted || (omitted && (warning == nil || !strings.HasPrefix(*warning, "raw omitted:"))) || (!omitted && warning != nil) {
			t.Errorf("row %d rawNull=%v warning=%v; omitted want %v", i, rawNull, warning, omitted)
		}
		if status != "ok" || description != "cleantext" || image != "https://images.example/token.png" || twitter != "https://x.com/account" {
			t.Errorf("row %d display fields %s %q %q %q", i, status, description, image, twitter)
		}
	}
	if delta := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("base", "raw_omitted")) - before; delta != 3 {
		t.Errorf("raw_omitted delta=%g; want 3", delta)
	}
}
