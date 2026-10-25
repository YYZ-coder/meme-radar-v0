package metadata

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"time"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/store"
)

// URISource is the read-only chain boundary used when parse has no URI.
type URISource interface {
	URI(context.Context, []byte) (string, error)
}

// Worker performs enrich's second step for one chain.
type Worker struct {
	store   *store.Store
	chain   string
	fetcher *Fetcher
	source  URISource
	log     *slog.Logger
}

// New creates a per-chain worker. Share fetcher across chains for one HTTP budget.
func New(st *store.Store, chain string, f *Fetcher, source URISource, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{store: st, chain: chain, fetcher: f, source: source, log: log}
}

// Step fetches at most 100 due tokens, persists isolated rows and backs off failures.
func (w *Worker) Step(ctx context.Context, now time.Time) error {
	ctx = context.WithValue(ctx, chainContextKey{}, w.chain)
	candidates, err := w.store.MetadataCandidates(ctx, w.chain, now, 100, w.fetcher.options.FourMemeAPIURL != "")
	if err != nil {
		return err
	}
	updates := make([]store.MetadataWrite, 0, len(candidates))
	for _, token := range candidates {
		started := time.Now()
		doc, uri, fetchErr := w.fetch(ctx, token)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(fetchErr, ErrDisabled) {
			continue
		}
		update := store.MetadataWrite{Address: token.Address, SourceURI: uri, Status: "ok", ImageURL: doc.ImageURL, Socials: doc.Socials, Description: doc.Description, Raw: doc.Raw, Attempts: token.Attempts + 1}
		if doc.RawOmitted != "" {
			update.Error = store.MetadataRawOmittedPrefix + doc.RawOmitted
		}
		outcome := "ok"
		if fetchErr != nil {
			update.Error = fetchErr.Error()
			switch {
			case errors.Is(fetchErr, ErrBlocked):
				update.Status = "skipped"
				outcome = "blocked"
				host := ""
				if parsed, err := url.Parse(uri); err == nil {
					host = parsed.Hostname()
				}
				var rejected *blockedAddressError
				if errors.As(fetchErr, &rejected) {
					host = rejected.host
				}
				w.log.Warn("metadata address blocked", "role", "enrich", "chain", w.chain, "host", host)
			case errors.Is(fetchErr, ErrNoSource):
				update.Status = "skipped"
				outcome = "skipped"
			default:
				update.Status = "failed"
				outcome = "failed"
				if update.Attempts < 5 {
					next := now.Add([]time.Duration{10 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}[update.Attempts-1])
					update.NextAttemptAt = &next
				}
			}
		}
		updates = append(updates, update)
		metrics.MetadataFetches.WithLabelValues(w.chain, outcome).Inc()
		metrics.MetadataFetchSeconds.WithLabelValues(w.chain).Observe(time.Since(started).Seconds())
	}
	return w.store.SaveMetadata(ctx, w.chain, updates, now)
}

func (w *Worker) fetch(ctx context.Context, token store.MetadataCandidate) (Document, string, error) {
	uri := token.URI
	if len(token.Address) != 20 && len(token.Address) != 32 {
		return Document{}, uri, ErrNoSource
	}
	if uri == "" {
		if token.Platform == "four_meme" {
			uri, err := w.fetcher.fourMemeURI(token.Address)
			if err != nil {
				return Document{}, uri, err
			}
			doc, err := w.fetcher.FetchFourMeme(ctx, token.Address)
			return doc, uri, err
		}
		if w.source == nil || (len(token.Address) != 32 && token.Platform != "doppler") {
			return Document{}, uri, ErrNoSource
		}
		var err error
		uri, err = w.source.URI(ctx, token.Address)
		if err != nil {
			return Document{}, uri, err
		}
	}
	normalized, err := NormalizeURI(uri, w.fetcher.gateway())
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return Document{}, uri, err
		}
		return Document{}, uri, errors.Join(ErrNoSource, err)
	}
	if token.Platform == "clanker" {
		image := displayURL(normalized, w.fetcher.gateway())
		if image == "" {
			return Document{}, uri, ErrNoSource
		}
		return Document{ImageURL: image, Socials: map[string]string{}}, uri, nil
	}
	doc, err := w.fetcher.Fetch(ctx, uri)
	return doc, uri, err
}
