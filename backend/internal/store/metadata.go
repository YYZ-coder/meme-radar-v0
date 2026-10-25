package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meme-radar/radar/internal/metrics"
)

// MetadataCandidate is a token with pending display metadata or a due retry.
type MetadataCandidate struct {
	Address       []byte
	URI, Platform string
	Attempts      int
}

// MetadataCandidates prioritizes activity in [now-24h,now), then other pending
// tokens. Failed attempts are eligible only when due and below the five-attempt cap.
// A disabled Four.meme API does not consume slots or mark its tokens terminal.
func (s *Store) MetadataCandidates(ctx context.Context, chain string, now time.Time, limit int, fourMemeEnabled bool) ([]MetadataCandidate, error) {
	rows, err := s.pool.Query(ctx, `
 SELECT t.address,coalesce(t.metadata_uri,''),coalesce(t.launch_platform,''),coalesce(m.attempts,0)
 FROM tokens t LEFT JOIN token_metadata m USING(chain,address)
 WHERE t.chain=$1 AND coalesce(m.attempts,0)<5
   AND (m.address IS NULL OR (m.status='pending' AND (m.next_attempt_at IS NULL OR m.next_attempt_at<=$2))
       OR (m.status='failed' AND m.next_attempt_at<=$2))
   AND ($4 OR t.launch_platform IS DISTINCT FROM 'four_meme' OR NULLIF(t.metadata_uri,'') IS NOT NULL)
 ORDER BY EXISTS(SELECT 1 FROM trades tr WHERE tr.chain=t.chain AND tr.token_address=t.address
   AND tr.block_time >= $2::timestamptz-interval '24 hours' AND tr.block_time < $2) DESC,t.address
 LIMIT $3`, chain, now, limit, fourMemeEnabled)
	if err != nil {
		return nil, fmt.Errorf("store: metadata candidates: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[MetadataCandidate])
	if err != nil {
		return nil, fmt.Errorf("store: metadata candidates: %w", err)
	}
	return out, nil
}

// MetadataWrite is one enrich-owned metadata outcome. No token facts are updated.
type MetadataWrite struct {
	Address       []byte            `json:"address"`
	SourceURI     string            `json:"source_uri"`
	Status        string            `json:"status"`
	ImageURL      string            `json:"image_url"`
	Socials       map[string]string `json:"socials"`
	Description   string            `json:"description"`
	Raw           json.RawMessage   `json:"raw"`
	Attempts      int               `json:"attempts"`
	NextAttemptAt *time.Time        `json:"next_attempt_at"`
	Error         string            `json:"error"`
}

// MetadataRawOmittedPrefix distinguishes successful display metadata with omitted raw.
const MetadataRawOmittedPrefix = "raw omitted: "

// SaveMetadata isolates each row so unusable raw or a bad sibling cannot roll
// back usable display metadata. Character errors receive one raw-only retry.
func (s *Store) SaveMetadata(ctx context.Context, chain string, updates []MetadataWrite, at time.Time) error {
	var failures []error
	for i, update := range updates {
		err := s.saveMetadataRow(ctx, chain, update, at)
		var pg *pgconn.PgError
		if len(update.Raw) > 0 && errors.As(err, &pg) && (pg.Code == "22P05" || pg.Code == "22P02" || pg.Code == "22025") {
			update.Raw = nil
			update.Error = MetadataRawOmittedPrefix + "SQLSTATE " + pg.Code
			err = s.saveMetadataRow(ctx, chain, update, at)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("store: save metadata row %d: %w", i, err))
			if ctx.Err() != nil {
				break
			}
		} else if len(update.Raw) == 0 && strings.HasPrefix(update.Error, MetadataRawOmittedPrefix) {
			metrics.MetadataFetches.WithLabelValues(chain, "raw_omitted").Inc()
		}
	}
	return errors.Join(failures...)
}

func (s *Store) saveMetadataRow(ctx context.Context, chain string, update MetadataWrite, at time.Time) error {
	var raw, socials any
	if len(update.Raw) > 0 {
		raw = string(update.Raw)
	}
	if update.Socials != nil {
		cleaned := make(map[string]string, len(update.Socials))
		for key, value := range update.Socials {
			cleaned[metadataText(key)] = metadataText(value)
		}
		data, err := json.Marshal(cleaned)
		if err != nil {
			return fmt.Errorf("encode metadata socials: %w", err)
		}
		socials = string(data)
	}
	_, err := s.pool.Exec(ctx, `
 INSERT INTO token_metadata(chain,address,source_uri,status,image_url,socials,description,raw,attempts,next_attempt_at,fetched_at,error)
 VALUES($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6::jsonb,NULLIF($7,''),$8::jsonb,$9,$10,$11,NULLIF($12,''))
 ON CONFLICT(chain,address) DO UPDATE SET source_uri=EXCLUDED.source_uri,status=EXCLUDED.status,image_url=EXCLUDED.image_url,socials=EXCLUDED.socials,
 description=EXCLUDED.description,raw=EXCLUDED.raw,attempts=EXCLUDED.attempts,next_attempt_at=EXCLUDED.next_attempt_at,fetched_at=EXCLUDED.fetched_at,error=EXCLUDED.error`,
		chain, update.Address, metadataText(update.SourceURI), update.Status, metadataText(update.ImageURL), socials, metadataText(update.Description), raw, update.Attempts, update.NextAttemptAt, at, metadataText(update.Error))
	return err
}

func metadataText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, "\ufffd"))
}
