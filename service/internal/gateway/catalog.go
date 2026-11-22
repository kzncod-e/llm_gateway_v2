package gateway

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"service/internal/pricing"
)

// ProviderEntry is a provider as seen by the hot path, with its decrypted key.
type ProviderEntry struct {
	ID       uuid.UUID
	Slug     string
	Name     string
	Type     string
	BaseURL  string
	APIKey   string
	Enabled  bool
	Priority int
	Timeout  time.Duration
}

type ModelEntry struct {
	ID          uuid.UUID
	Name        string
	DisplayName string
	Enabled     bool
	Provider    *ProviderEntry
	Prices      pricing.Versions
}

type PolicyEntry struct {
	ID      uuid.UUID
	Alias   string
	Enabled bool
	Retry   RetryPolicy
	Targets []*ModelEntry
}

// Snapshot is an immutable view of routing configuration.
type Snapshot struct {
	Providers       map[uuid.UUID]*ProviderEntry
	ProvidersBySlug map[string]*ProviderEntry
	Models          map[uuid.UUID]*ModelEntry
	ModelsByName    map[string][]*ModelEntry
	Policies        map[string]*PolicyEntry
	LoadedAt        time.Time
}

type ModelRow struct {
	ID, ProviderID    uuid.UUID
	Name, DisplayName string
	Enabled           bool
}

type PolicyRow struct {
	ID                   uuid.UUID
	Alias                string
	Enabled              bool
	MaxAttemptsPerTarget int
	RetryBaseMs          int
	RetryMaxMs           int
	TargetModelIDs       []uuid.UUID
}

// SnapshotData is the raw input used to build a snapshot.
type SnapshotData struct {
	Providers []ProviderEntry
	Models    []ModelRow
	Prices    []pricing.Price
	Policies  []PolicyRow
}

func BuildSnapshot(d SnapshotData, now time.Time) *Snapshot {
	s := &Snapshot{
		Providers:       map[uuid.UUID]*ProviderEntry{},
		ProvidersBySlug: map[string]*ProviderEntry{},
		Models:          map[uuid.UUID]*ModelEntry{},
		ModelsByName:    map[string][]*ModelEntry{},
		Policies:        map[string]*PolicyEntry{},
		LoadedAt:        now,
	}
	for i := range d.Providers {
		p := d.Providers[i]
		s.Providers[p.ID] = &p
		s.ProvidersBySlug[p.Slug] = &p
	}
	prices := map[uuid.UUID][]pricing.Price{}
	for _, p := range d.Prices {
		prices[p.ModelID] = append(prices[p.ModelID], p)
	}
	for _, m := range d.Models {
		prov := s.Providers[m.ProviderID]
		if prov == nil {
			continue
		}
		e := &ModelEntry{ID: m.ID, Name: m.Name, DisplayName: m.DisplayName, Enabled: m.Enabled, Provider: prov, Prices: pricing.NewVersions(prices[m.ID])}
		s.Models[m.ID] = e
		s.ModelsByName[m.Name] = append(s.ModelsByName[m.Name], e)
	}
	for name := range s.ModelsByName {
		list := s.ModelsByName[name]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Provider.Priority != list[j].Provider.Priority {
				return list[i].Provider.Priority < list[j].Provider.Priority
			}
			return list[i].Provider.Slug < list[j].Provider.Slug
		})
	}
	for _, p := range d.Policies {
		pe := &PolicyEntry{ID: p.ID, Alias: p.Alias, Enabled: p.Enabled, Retry: DefaultRetryPolicy()}
		pe.Retry.MaxAttemptsPerTarget = p.MaxAttemptsPerTarget
		pe.Retry.Base = time.Duration(p.RetryBaseMs) * time.Millisecond
		pe.Retry.Max = time.Duration(p.RetryMaxMs) * time.Millisecond
		for _, id := range p.TargetModelIDs {
			if m := s.Models[id]; m != nil {
				pe.Targets = append(pe.Targets, m)
			}
		}
		s.Policies[p.Alias] = pe
	}
	return s
}

func (m *ModelEntry) usable() bool { return m.Enabled && m.Provider.Enabled }

// PublicModels lists model identifiers a client may request.
func (s *Snapshot) PublicModels() []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for alias, p := range s.Policies {
		if p.Enabled {
			add(alias)
		}
	}
	for _, m := range s.Models {
		if m.usable() {
			add(m.Name)
			add(m.Provider.Slug + "/" + m.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Catalog holds the current snapshot behind an atomic pointer.
type Catalog struct {
	ptr  atomic.Pointer[Snapshot]
	load func(ctx context.Context) (*Snapshot, error)
	log  *slog.Logger
}

func NewCatalog(load func(ctx context.Context) (*Snapshot, error), log *slog.Logger) *Catalog {
	c := &Catalog{load: load, log: log}
	c.ptr.Store(BuildSnapshot(SnapshotData{}, time.Now()))
	return c
}

// NewStaticCatalog wraps a fixed snapshot (tests).
func NewStaticCatalog(s *Snapshot) *Catalog {
	c := &Catalog{log: slog.Default()}
	c.ptr.Store(s)
	return c
}

func (c *Catalog) Snapshot() *Snapshot { return c.ptr.Load() }

func (c *Catalog) Reload(ctx context.Context) error {
	if c.load == nil {
		return nil
	}
	s, err := c.load(ctx)
	if err != nil {
		return err
	}
	c.ptr.Store(s)
	return nil
}

// Run refreshes the snapshot periodically so changes made by other instances
// are picked up.
func (c *Catalog) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := c.Reload(rctx); err != nil && ctx.Err() == nil {
				c.log.Error("catalog refresh failed", "error", err)
			}
			cancel()
		}
	}
}

// splitExplicit parses "provider-slug/model-name".
func splitExplicit(model string) (slug, name string, ok bool) {
	slug, name, ok = strings.Cut(model, "/")
	if !ok || slug == "" || name == "" {
		return "", "", false
	}
	return slug, name, true
}
