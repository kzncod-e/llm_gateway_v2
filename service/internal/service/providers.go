package service

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"service/internal/gateway"
	"service/internal/model"
	"service/internal/provider/anthropic"
	"service/internal/provider/gemini"
	"service/internal/provider/openai"
	"service/internal/repository"
	"service/internal/security"
)

type ProviderService struct {
	repo    *repository.Repo
	audit   *Auditor
	box     *security.SecretBox
	catalog *gateway.Catalog
	log     *slog.Logger
}

func NewProviderService(repo *repository.Repo, audit *Auditor, box *security.SecretBox, catalog *gateway.Catalog, log *slog.Logger) *ProviderService {
	return &ProviderService{repo: repo, audit: audit, box: box, catalog: catalog, log: log}
}

// Decrypt opens a stored provider credential; the provider ID is bound as AAD.
func (s *ProviderService) Decrypt(c repository.ProviderCredential) (string, error) {
	pt, err := s.box.Open(c.Ciphertext, []byte(c.ID))
	return string(pt), err
}

// LoadCatalog builds a fresh routing snapshot (used by the catalog).
func (s *ProviderService) LoadCatalog(ctx context.Context) (*gateway.Snapshot, error) {
	snap, warnings, err := s.repo.LoadCatalog(ctx, s.Decrypt)
	for _, w := range warnings {
		s.log.Error("catalog warning", "error", w)
	}
	return snap, err
}

func (s *ProviderService) reload(ctx context.Context) {
	if err := s.catalog.Reload(ctx); err != nil {
		s.log.Error("catalog reload after change failed", "error", err)
	}
}

func defaultBaseURL(typ string) string {
	switch typ {
	case "openai":
		return openai.DefaultBaseURL
	case "anthropic":
		return anthropic.DefaultBaseURL
	case "gemini":
		return gemini.DefaultBaseURL
	}
	return ""
}

type ProviderInput struct {
	Slug      *string `json:"slug"`
	Name      *string `json:"name"`
	Type      *string `json:"type"`
	BaseURL   *string `json:"base_url"`
	APIKey    *string `json:"api_key"` // write-only
	Enabled   *bool   `json:"enabled"`
	Priority  *int    `json:"priority"`
	TimeoutMs *int    `json:"timeout_ms"`
}

func (s *ProviderService) seal(id uuid.UUID, key string) ([]byte, string, error) {
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 500 {
		return nil, "", Invalid("api_key must be between 8 and 500 characters")
	}
	ct, err := s.box.Seal([]byte(key), []byte(id.String()))
	return ct, security.Last4(key), err
}

func validateProviderNumbers(priority, timeout *int) error {
	if priority != nil && (*priority < 0 || *priority > 10000) {
		return Invalid("priority must be between 0 and 10000")
	}
	if timeout != nil && (*timeout < 1000 || *timeout > 600000) {
		return Invalid("timeout_ms must be between 1000 and 600000")
	}
	return nil
}

func (s *ProviderService) Create(ctx context.Context, p *Principal, in ProviderInput) (*model.Provider, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	if in.Type == nil || !slices.Contains(model.ProviderTypes, *in.Type) {
		return nil, Invalid("type must be one of %s", strings.Join(model.ProviderTypes, ", "))
	}
	if in.Name == nil {
		return nil, Invalid("name is required")
	}
	name, err := cleanName("name", *in.Name, 100)
	if err != nil {
		return nil, err
	}
	slug := Slugify(name)
	if in.Slug != nil && *in.Slug != "" {
		slug = *in.Slug
	}
	if !validSlug(slug) {
		return nil, Invalid("slug must be 2-50 lowercase letters, digits or dashes")
	}
	pr := &model.Provider{ID: model.NewID(), Slug: slug, Name: name, Type: *in.Type, BaseURL: defaultBaseURL(*in.Type), Enabled: true, Priority: 100, TimeoutMs: 60000}
	if in.BaseURL != nil && *in.BaseURL != "" {
		pr.BaseURL = strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
	}
	if !validBaseURL(pr.BaseURL) {
		return nil, Invalid("base_url must be an http(s) URL without query string")
	}
	if err := validateProviderNumbers(in.Priority, in.TimeoutMs); err != nil {
		return nil, err
	}
	if in.Priority != nil {
		pr.Priority = *in.Priority
	}
	if in.TimeoutMs != nil {
		pr.TimeoutMs = *in.TimeoutMs
	}
	if in.Enabled != nil {
		pr.Enabled = *in.Enabled
	}
	if in.APIKey != nil && *in.APIKey != "" {
		ct, last4, err := s.seal(pr.ID, *in.APIKey)
		if err != nil {
			return nil, err
		}
		pr.APIKeyCiphertext, pr.APIKeyLast4, pr.HasAPIKey = ct, last4, true
	}
	if err := s.repo.CreateProvider(ctx, pr); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "provider.created", "provider", pr.ID.String(), map[string]any{"slug": pr.Slug, "type": pr.Type, "base_url": pr.BaseURL})
	s.reload(ctx)
	return pr, nil
}

func (s *ProviderService) List(ctx context.Context) ([]*model.Provider, error) { return s.repo.ListProviders(ctx) }

func (s *ProviderService) Get(ctx context.Context, id uuid.UUID) (*model.Provider, error) {
	pr, err := s.repo.Provider(ctx, id)
	return pr, Translate(err)
}

func (s *ProviderService) Update(ctx context.Context, p *Principal, id uuid.UUID, in ProviderInput) (*model.Provider, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	if in.Type != nil || in.Slug != nil {
		return nil, Invalid("type and slug cannot be changed")
	}
	up := repository.ProviderUpdate{Enabled: in.Enabled, Priority: in.Priority, TimeoutMs: in.TimeoutMs}
	if in.Name != nil {
		n, err := cleanName("name", *in.Name, 100)
		if err != nil {
			return nil, err
		}
		up.Name = &n
	}
	if in.BaseURL != nil {
		u := strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
		if !validBaseURL(u) {
			return nil, Invalid("base_url must be an http(s) URL without query string")
		}
		up.BaseURL = &u
	}
	if err := validateProviderNumbers(in.Priority, in.TimeoutMs); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateProvider(ctx, id, up); err != nil {
		return nil, Translate(err)
	}
	if in.APIKey != nil {
		if err := s.setCredential(ctx, id, *in.APIKey); err != nil {
			return nil, err
		}
	}
	s.audit.Record(ctx, p, "provider.updated", "provider", id.String(), map[string]any{"enabled": in.Enabled, "priority": in.Priority, "credential_rotated": in.APIKey != nil})
	s.reload(ctx)
	return s.Get(ctx, id)
}

func (s *ProviderService) setCredential(ctx context.Context, id uuid.UUID, key string) error {
	ct, last4, err := s.seal(id, key)
	if err != nil {
		return err
	}
	return Translate(s.repo.SetProviderCredential(ctx, id, ct, last4))
}

func (s *ProviderService) Delete(ctx context.Context, p *Principal, id uuid.UUID) error {
	if err := p.RequireAdmin(); err != nil {
		return err
	}
	if err := s.repo.DeleteProvider(ctx, id); err != nil {
		return Translate(err)
	}
	s.audit.Record(ctx, p, "provider.deleted", "provider", id.String(), nil)
	s.reload(ctx)
	return nil
}

// Models --------------------------------------------------------------------------------

type ModelInput struct {
	ProviderID  *uuid.UUID `json:"provider_id"`
	Name        *string    `json:"name"`
	DisplayName *string    `json:"display_name"`
	Enabled     *bool      `json:"enabled"`
}

func validModelName(s string) bool {
	if s == "" || len(s) > 200 || strings.ContainsAny(s, " /\t\n?#") {
		return false
	}
	return true
}

func (s *ProviderService) Models(ctx context.Context, providerID *uuid.UUID) ([]*model.Model, error) {
	return s.repo.ListModels(ctx, providerID)
}

func (s *ProviderService) CreateModel(ctx context.Context, p *Principal, in ModelInput) (*model.Model, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	if in.ProviderID == nil || in.Name == nil || !validModelName(strings.TrimSpace(*in.Name)) {
		return nil, Invalid("provider_id and a model name (no spaces or slashes) are required")
	}
	m := &model.Model{ID: model.NewID(), ProviderID: *in.ProviderID, Name: strings.TrimSpace(*in.Name), Enabled: true}
	if in.DisplayName != nil {
		m.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if err := s.repo.CreateModel(ctx, m); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "model.created", "model", m.ID.String(), map[string]any{"provider_id": m.ProviderID, "name": m.Name})
	s.reload(ctx)
	return s.repo.Model(ctx, m.ID)
}

func (s *ProviderService) UpdateModel(ctx context.Context, p *Principal, id uuid.UUID, in ModelInput) (*model.Model, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	if in.Name != nil || in.ProviderID != nil {
		return nil, Invalid("model name and provider cannot be changed; create a new model instead")
	}
	if err := s.repo.UpdateModel(ctx, id, repository.ModelUpdate{DisplayName: in.DisplayName, Enabled: in.Enabled}); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "model.updated", "model", id.String(), map[string]any{"enabled": in.Enabled})
	s.reload(ctx)
	m, err := s.repo.Model(ctx, id)
	return m, Translate(err)
}

func (s *ProviderService) DeleteModel(ctx context.Context, p *Principal, id uuid.UUID) error {
	if err := p.RequireAdmin(); err != nil {
		return err
	}
	if err := s.repo.DeleteModel(ctx, id); err != nil {
		return Translate(err)
	}
	s.audit.Record(ctx, p, "model.deleted", "model", id.String(), nil)
	s.reload(ctx)
	return nil
}

// Pricing --------------------------------------------------------------------------------

type PriceInput struct {
	EffectiveFrom   *time.Time `json:"effective_from"`
	InputPerM       *int64     `json:"input_per_mtok_nanos"`
	CachedInputPerM *int64     `json:"cached_input_per_mtok_nanos"`
	CacheWritePerM  *int64     `json:"cache_write_per_mtok_nanos"`
	OutputPerM      *int64     `json:"output_per_mtok_nanos"`
}

const maxPricePerM = 10_000 * 1_000_000_000 // $10,000 per 1M tokens: sanity bound

func (s *ProviderService) Prices(ctx context.Context, modelID uuid.UUID) ([]*model.ModelPrice, error) {
	return s.repo.ListPrices(ctx, modelID)
}

// AddPrice appends a new price version. Versions are immutable so historical
// costs stay reproducible.
func (s *ProviderService) AddPrice(ctx context.Context, p *Principal, modelID uuid.UUID, in PriceInput) (*model.ModelPrice, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	if in.InputPerM == nil || in.OutputPerM == nil {
		return nil, Invalid("input_per_mtok_nanos and output_per_mtok_nanos are required")
	}
	pr := &model.ModelPrice{ID: model.NewID(), ModelID: modelID, EffectiveFrom: time.Now().UTC().Truncate(time.Second), InputPerM: *in.InputPerM, OutputPerM: *in.OutputPerM}
	if in.EffectiveFrom != nil {
		pr.EffectiveFrom = in.EffectiveFrom.UTC()
	}
	// Cached input defaults to the input price and cache writes to input price
	// unless set, so no category is ever silently free.
	pr.CachedInputPerM, pr.CacheWritePerM = pr.InputPerM, pr.InputPerM
	if in.CachedInputPerM != nil {
		pr.CachedInputPerM = *in.CachedInputPerM
	}
	if in.CacheWritePerM != nil {
		pr.CacheWritePerM = *in.CacheWritePerM
	}
	for _, v := range []int64{pr.InputPerM, pr.OutputPerM, pr.CachedInputPerM, pr.CacheWritePerM} {
		if v < 0 || v > maxPricePerM {
			return nil, Invalid("prices must be between 0 and %d nanos per 1M tokens", int64(maxPricePerM))
		}
	}
	if err := s.repo.AddPrice(ctx, pr, &p.User.ID); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "pricing.added", "model", modelID.String(), map[string]any{"effective_from": pr.EffectiveFrom, "input": pr.InputPerM, "output": pr.OutputPerM})
	s.reload(ctx)
	return pr, nil
}

// Routing policies ---------------------------------------------------------------------

type PolicyInput struct {
	Alias                string      `json:"alias"`
	Description          string      `json:"description"`
	MaxAttemptsPerTarget *int        `json:"max_attempts_per_target"`
	RetryBaseMs          *int        `json:"retry_base_ms"`
	RetryMaxMs           *int        `json:"retry_max_ms"`
	Enabled              *bool       `json:"enabled"`
	TargetModelIDs       []uuid.UUID `json:"target_model_ids"`
}

func (s *ProviderService) Policies(ctx context.Context) ([]*model.RoutingPolicy, error) {
	return s.repo.ListPolicies(ctx)
}

func (s *ProviderService) SavePolicy(ctx context.Context, p *Principal, id *uuid.UUID, in PolicyInput) (*model.RoutingPolicy, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	in.Alias = strings.TrimSpace(in.Alias)
	if !validModelName(in.Alias) {
		return nil, Invalid("alias is required and may not contain spaces or slashes")
	}
	if len(in.TargetModelIDs) == 0 || len(in.TargetModelIDs) > 10 {
		return nil, Invalid("a policy needs between 1 and 10 target models")
	}
	seen := map[uuid.UUID]bool{}
	for _, t := range in.TargetModelIDs {
		if seen[t] {
			return nil, Invalid("target models must be unique")
		}
		seen[t] = true
	}
	pol := &model.RoutingPolicy{Alias: in.Alias, Description: strings.TrimSpace(in.Description), MaxAttemptsPerTarget: 2, RetryBaseMs: 250, RetryMaxMs: 2000,
		Enabled: true, TargetModelIDs: in.TargetModelIDs}
	if in.MaxAttemptsPerTarget != nil {
		pol.MaxAttemptsPerTarget = *in.MaxAttemptsPerTarget
	}
	if in.RetryBaseMs != nil {
		pol.RetryBaseMs = *in.RetryBaseMs
	}
	if in.RetryMaxMs != nil {
		pol.RetryMaxMs = *in.RetryMaxMs
	}
	if in.Enabled != nil {
		pol.Enabled = *in.Enabled
	}
	if pol.MaxAttemptsPerTarget < 1 || pol.MaxAttemptsPerTarget > 5 || pol.RetryBaseMs < 0 || pol.RetryBaseMs > 10000 || pol.RetryMaxMs < pol.RetryBaseMs || pol.RetryMaxMs > 30000 {
		return nil, Invalid("max_attempts_per_target must be 1-5 and 0 <= retry_base_ms <= retry_max_ms <= 30000")
	}
	create := id == nil
	if create {
		pol.ID = model.NewID()
	} else {
		pol.ID = *id
	}
	if err := s.repo.SavePolicy(ctx, pol, create); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "routing_policy.saved", "routing_policy", pol.ID.String(), map[string]any{"alias": pol.Alias, "targets": pol.TargetModelIDs})
	s.reload(ctx)
	return pol, nil
}

func (s *ProviderService) DeletePolicy(ctx context.Context, p *Principal, id uuid.UUID) error {
	if err := p.RequireAdmin(); err != nil {
		return err
	}
	if err := s.repo.DeletePolicy(ctx, id); err != nil {
		return Translate(err)
	}
	s.audit.Record(ctx, p, "routing_policy.deleted", "routing_policy", id.String(), nil)
	s.reload(ctx)
	return nil
}
