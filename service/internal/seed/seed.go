// Package seed creates demo data that points at the bundled mock provider so the
// whole system can be explored without real provider credentials.
package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"service/internal/model"
	"service/internal/repository"
	"service/internal/security"
)

type Options struct {
	MockBaseURL string
	DemoAPIKey  string // optional raw gateway key (gwk_...) to create for the demo team
	AdminEmail  string
}

type demoModel struct {
	name, display                 string
	in, cachedIn, cacheWrite, out float64 // USD per 1M tokens (demo values; verify with provider pricing pages)
}

func usd(v float64) int64 { return int64(v * 1e9) }

// Demo seeds providers, models, prices, routing policies and a team, once.
func Demo(ctx context.Context, repo *repository.Repo, box *security.SecretBox, hasher *security.KeyHasher, o Options, log *slog.Logger) error {
	existing, err := repo.ListProviders(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	base := strings.TrimRight(o.MockBaseURL, "/")
	type demoProvider struct {
		slug, name, typ, path string
		priority              int
		models                []demoModel
	}
	providers := []demoProvider{
		{"openai-mock", "OpenAI (mock)", "openai", "/openai", 10, []demoModel{
			{"gpt-4o", "GPT-4o", 2.50, 1.25, 2.50, 10.00},
			{"gpt-4o-mini", "GPT-4o mini", 0.15, 0.075, 0.15, 0.60},
			{"mock-fail-503", "Always fails (503)", 1, 1, 1, 1},
			{"mock-drop-stream", "Drops streams mid-way", 1, 1, 1, 1},
		}},
		{"anthropic-mock", "Anthropic (mock)", "anthropic", "/anthropic", 20, []demoModel{
			{"claude-sonnet-4-5", "Claude Sonnet 4.5", 3.00, 0.30, 3.75, 15.00},
			{"claude-haiku-4-5", "Claude Haiku 4.5", 1.00, 0.10, 1.25, 5.00},
		}},
		{"gemini-mock", "Gemini (mock)", "gemini", "/gemini", 30, []demoModel{
			{"gemini-2.5-flash", "Gemini 2.5 Flash", 0.30, 0.075, 0.30, 2.50},
		}},
	}
	modelIDs := map[string]uuid.UUID{}
	effective := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, dp := range providers {
		p := &model.Provider{ID: model.NewID(), Slug: dp.slug, Name: dp.name, Type: dp.typ, BaseURL: base + dp.path, Enabled: true, Priority: dp.priority, TimeoutMs: 30000}
		ct, err := box.Seal([]byte("mock-key-"+dp.slug), []byte(p.ID.String()))
		if err != nil {
			return err
		}
		p.APIKeyCiphertext, p.APIKeyLast4 = ct, security.Last4("mock-key-"+dp.slug)
		if err := repo.CreateProvider(ctx, p); err != nil {
			return fmt.Errorf("seed provider %s: %w", dp.slug, err)
		}
		for _, dm := range dp.models {
			m := &model.Model{ID: model.NewID(), ProviderID: p.ID, Name: dm.name, DisplayName: dm.display, Enabled: true}
			if err := repo.CreateModel(ctx, m); err != nil {
				return err
			}
			modelIDs[dp.slug+"/"+dm.name] = m.ID
			price := &model.ModelPrice{ID: model.NewID(), ModelID: m.ID, EffectiveFrom: effective,
				InputPerM: usd(dm.in), CachedInputPerM: usd(dm.cachedIn), CacheWritePerM: usd(dm.cacheWrite), OutputPerM: usd(dm.out)}
			if err := repo.AddPrice(ctx, price, nil); err != nil {
				return err
			}
		}
	}

	policies := []struct {
		alias, desc string
		targets     []string
	}{
		{"smart", "GPT-4o with Claude Sonnet fallback", []string{"openai-mock/gpt-4o", "anthropic-mock/claude-sonnet-4-5"}},
		{"fast", "Gemini Flash with GPT-4o mini fallback", []string{"gemini-mock/gemini-2.5-flash", "openai-mock/gpt-4o-mini"}},
		{"fallback-demo", "Primary always fails (503) to demonstrate fallback", []string{"openai-mock/mock-fail-503", "anthropic-mock/claude-haiku-4-5"}},
	}
	for _, pd := range policies {
		pol := &model.RoutingPolicy{ID: model.NewID(), Alias: pd.alias, Description: pd.desc, MaxAttemptsPerTarget: 2, RetryBaseMs: 100, RetryMaxMs: 1000, Enabled: true}
		for _, t := range pd.targets {
			pol.TargetModelIDs = append(pol.TargetModelIDs, modelIDs[t])
		}
		if err := repo.SavePolicy(ctx, pol, true); err != nil {
			return err
		}
	}

	team := &model.Team{ID: model.NewID(), Name: "Demo Team", Slug: "demo", Description: "Seeded demo team (mock providers)"}
	if err := repo.CreateTeam(ctx, team); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil
		}
		return err
	}
	monthly := usd(25)
	if err := repo.UpsertTeamBudget(ctx, &model.TeamBudget{TeamID: team.ID, MonthlyLimit: &monthly, WarnPct: 80}); err != nil {
		return err
	}
	if o.AdminEmail != "" {
		if u, err := repo.UserByEmail(ctx, o.AdminEmail); err == nil {
			if err := repo.UpsertMember(ctx, team.ID, u.ID, model.TeamRoleOwner); err != nil {
				return err
			}
		}
	}
	if o.DemoAPIKey != "" {
		if !security.LooksLikeAPIKey(o.DemoAPIKey) {
			return errors.New("DEMO_API_KEY must look like gwk_ followed by 43 base64url characters")
		}
		k := &model.APIKey{ID: model.NewID(), TeamID: team.ID, Name: "Demo key (from DEMO_API_KEY)", Prefix: o.DemoAPIKey[:12]}
		if err := repo.CreateAPIKey(ctx, k, hasher.Hash(o.DemoAPIKey)); err != nil {
			return err
		}
	}
	log.Info("demo data seeded", "providers", len(providers), "team", team.Slug)
	return nil
}
