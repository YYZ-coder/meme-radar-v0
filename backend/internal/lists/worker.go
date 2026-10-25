// Package lists builds immutable smart-money snapshots for the compute role.
package lists

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/store"
)

// Worker claims at most one eligible list build per Step. Callers may run
// independent workers concurrently; PostgreSQL arbitrates ownership.
type Worker struct {
	store       *store.Store
	log         *slog.Logger
	lastArchive time.Time
}

// New returns a compute-owned list builder. A nil logger disables progress logs.
func New(st *store.Store, log *slog.Logger) *Worker { return &Worker{store: st, log: log} }

// Step builds and activates one version whose parent is currently active.
func (w *Worker) Step(ctx context.Context, now time.Time) error {
	buildErr := w.buildOne(ctx, now)
	var archiveErr error
	if w.lastArchive.IsZero() || now.Sub(w.lastArchive) >= time.Hour {
		archiveErr = w.store.ArchiveListImports(ctx, now)
		if archiveErr == nil {
			w.lastArchive = now
		}
	}
	return errors.Join(buildErr, archiveErr)
}

func (w *Worker) buildOne(ctx context.Context, now time.Time) error {
	start := time.Now()
	j, ok, err := w.store.ClaimListBuild(ctx, now)
	if err != nil || !ok {
		return err
	}
	defer func() { metrics.ListBuildSeconds.Observe(time.Since(start).Seconds()) }()
	if w.log != nil {
		w.log.Info("list build started", "role", "compute", "chain", "all", "version", j.Version, "members", j.TotalMembers, "took", time.Since(start).String())
	}
	members, err := w.build(ctx, j)
	if err == nil {
		err = w.store.PrepareListBuild(ctx, j, members, now.Add(time.Since(start)))
	}
	if err == nil {
		err = w.store.ActivateListBuild(ctx, j, members, now.Add(time.Since(start)))
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, store.ErrJobLeaseLost) {
			return err
		}
		failed := w.store.FailListBuild(ctx, j, now.Add(time.Since(start)))
		if failed != nil {
			return errors.Join(err, failed)
		}
		metrics.ListBuilds.WithLabelValues("failed").Inc()
		if w.log != nil {
			w.log.Error("list build failed", "role", "compute", "chain", "all", "version", j.Version, "members", len(members), "took", time.Since(start).String(), "error", err)
		}
		return err
	}
	metrics.ListBuilds.WithLabelValues("activated").Inc()
	if w.log != nil {
		w.log.Info("list build activated", "role", "compute", "chain", "all", "version", j.Version, "members", len(members), "took", time.Since(start).String())
	}
	return nil
}

// build uses immutable parent snapshots and versioned operations only.
func (w *Worker) build(ctx context.Context, j store.ListJob) ([]store.ListMember, error) {
	members, changes, err := w.store.ListBuildInput(ctx, j)
	if err != nil {
		return nil, err
	}
	indices := make(map[string]int, len(members))
	identity := func(chain string, address []byte) string { return chain + ":" + string(address) }
	for i, m := range members {
		indices[identity(m.Chain, m.Address)] = i
	}
	for _, c := range changes {
		key := identity(c.Chain, c.Address)
		if c.Op == "exclude" || c.Op == "restore" {
			if i, ok := indices[key]; ok {
				members[i].Excluded = c.Op == "exclude"
			}
			continue
		}
		if c.Op == "remove" {
			if i, ok := indices[key]; ok {
				m := &members[i]
				m.Sources = slices.DeleteFunc(m.Sources, func(source string) bool { return source == "import" })
				if len(m.Sources) == 0 {
					members = slices.Delete(members, i, i+1)
					clear(indices)
					for i, m := range members {
						indices[identity(m.Chain, m.Address)] = i
					}
				}
			}
			continue
		}
		if i, ok := indices[key]; ok {
			m := &members[i]
			if !slices.Contains(m.Sources, "import") {
				m.Sources = append(m.Sources, "import")
			}
			for _, tag := range c.Tags {
				if !slices.Contains(m.Tags, tag) {
					m.Tags = append(m.Tags, tag)
				}
			}
			slices.Sort(m.Tags)
			if m.Label == nil {
				m.Label = c.Label
			}
			if m.Twitter == nil {
				m.Twitter = c.Twitter
			}
			continue
		}
		tags := slices.Clone(c.Tags)
		slices.Sort(tags)
		indices[key] = len(members)
		members = append(members, store.ListMember{Chain: c.Chain, Address: c.Address, Label: c.Label, Tags: tags, Twitter: c.Twitter, Sources: []string{"import"}, AddedAt: j.CreatedAt})
	}
	exclusions, err := w.store.ListExclusions(ctx, j.Version)
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(exclusions))
	for _, e := range exclusions {
		excluded[identity(e.Chain, e.Address)] = true
	}
	for i := range members {
		m := &members[i]
		m.Excluded = excluded[identity(m.Chain, m.Address)]
	}
	return members, nil
}
