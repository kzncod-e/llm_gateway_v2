package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"service/internal/budget"
	"service/internal/model"
	"service/internal/repository"
	"service/internal/security"
)

type TeamService struct {
	repo    *repository.Repo
	audit   *Auditor
	keys    *KeyAuth
	hasher  *security.KeyHasher
	budgets *budget.Service
}

func NewTeamService(repo *repository.Repo, audit *Auditor, keys *KeyAuth, hasher *security.KeyHasher, budgets *budget.Service) *TeamService {
	return &TeamService{repo: repo, audit: audit, keys: keys, hasher: hasher, budgets: budgets}
}

type TeamInput struct {
	Name             *string `json:"name"`
	Slug             *string `json:"slug"`
	Description      *string `json:"description"`
	RPMLimit         *int    `json:"rpm_limit"`
	ConcurrencyLimit *int    `json:"concurrency_limit"`
	// ClearRPMLimit / ClearConcurrencyLimit reset limits to the gateway default.
	ClearRPMLimit         bool  `json:"clear_rpm_limit"`
	ClearConcurrencyLimit bool  `json:"clear_concurrency_limit"`
	Archived              *bool `json:"archived"`
}

func validateLimit(field string, v *int) error {
	if v != nil && (*v < 1 || *v > 1_000_000) {
		return Invalid("%s must be between 1 and 1000000", field)
	}
	return nil
}

func (s *TeamService) Create(ctx context.Context, p *Principal, in TeamInput) (*model.Team, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
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
		slug = strings.TrimSpace(*in.Slug)
	}
	if !validSlug(slug) {
		return nil, Invalid("slug must be 2-50 lowercase letters, digits or dashes")
	}
	if err := validateLimit("rpm_limit", in.RPMLimit); err != nil {
		return nil, err
	}
	if err := validateLimit("concurrency_limit", in.ConcurrencyLimit); err != nil {
		return nil, err
	}
	t := &model.Team{ID: model.NewID(), Name: name, Slug: slug, RPMLimit: in.RPMLimit, ConcurrencyLimit: in.ConcurrencyLimit}
	if in.Description != nil {
		t.Description = strings.TrimSpace(*in.Description)
		if len(t.Description) > 500 {
			return nil, Invalid("description must be at most 500 characters")
		}
	}
	if err := s.repo.CreateTeam(ctx, t); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "team.created", "team", t.ID.String(), map[string]any{"name": t.Name, "slug": t.Slug})
	return t, nil
}

func (s *TeamService) List(ctx context.Context, p *Principal) ([]*model.Team, error) {
	return s.repo.ListTeams(ctx, p.VisibleTeams())
}

func (s *TeamService) Get(ctx context.Context, p *Principal, id uuid.UUID) (*model.Team, error) {
	if err := p.Require(id, model.TeamRoleViewer); err != nil {
		return nil, err
	}
	t, err := s.repo.Team(ctx, id)
	return t, Translate(err)
}

func (s *TeamService) Update(ctx context.Context, p *Principal, id uuid.UUID, in TeamInput) (*model.Team, error) {
	if err := p.Require(id, model.TeamRoleOwner); err != nil {
		return nil, err
	}
	limitsChanged := in.RPMLimit != nil || in.ConcurrencyLimit != nil || in.ClearRPMLimit || in.ClearConcurrencyLimit || in.Archived != nil
	if limitsChanged && !p.IsAdmin() {
		return nil, ErrForbidden // limits and archival are platform decisions
	}
	up := repository.TeamUpdate{Archived: in.Archived}
	if in.Name != nil {
		n, err := cleanName("name", *in.Name, 100)
		if err != nil {
			return nil, err
		}
		up.Name = &n
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if len(d) > 500 {
			return nil, Invalid("description must be at most 500 characters")
		}
		up.Description = &d
	}
	if err := validateLimit("rpm_limit", in.RPMLimit); err != nil {
		return nil, err
	}
	if err := validateLimit("concurrency_limit", in.ConcurrencyLimit); err != nil {
		return nil, err
	}
	if in.ClearRPMLimit {
		var none *int
		up.RPMLimit = &none
	} else if in.RPMLimit != nil {
		up.RPMLimit = &in.RPMLimit
	}
	if in.ClearConcurrencyLimit {
		var none *int
		up.ConcurrencyLimit = &none
	} else if in.ConcurrencyLimit != nil {
		up.ConcurrencyLimit = &in.ConcurrencyLimit
	}
	if err := s.repo.UpdateTeam(ctx, id, up); err != nil {
		return nil, Translate(err)
	}
	s.keys.Invalidate(nil, &id) // limits/archival are cached with keys
	s.audit.Record(ctx, p, "team.updated", "team", id.String(), map[string]any{"rpm_limit": in.RPMLimit, "concurrency_limit": in.ConcurrencyLimit, "archived": in.Archived})
	t, err := s.repo.Team(ctx, id)
	return t, Translate(err)
}

// Members -------------------------------------------------------------------------------

type MemberInput struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func validTeamRole(r string) bool {
	return r == model.TeamRoleOwner || r == model.TeamRoleDeveloper || r == model.TeamRoleViewer
}

func (s *TeamService) Members(ctx context.Context, p *Principal, team uuid.UUID) ([]*model.TeamMember, error) {
	if err := p.Require(team, model.TeamRoleViewer); err != nil {
		return nil, err
	}
	return s.repo.ListMembers(ctx, team)
}

func (s *TeamService) AddMember(ctx context.Context, p *Principal, team uuid.UUID, in MemberInput) error {
	if err := p.Require(team, model.TeamRoleOwner); err != nil {
		return err
	}
	if !validTeamRole(in.Role) {
		return Invalid("role must be owner, developer or viewer")
	}
	u, err := s.repo.UserByEmail(ctx, strings.TrimSpace(in.Email))
	if errors.Is(err, repository.ErrNotFound) {
		return Invalid("no user with email %q; an administrator must create the account first", in.Email)
	}
	if err != nil {
		return err
	}
	if err := s.repo.UpsertMember(ctx, team, u.ID, in.Role); err != nil {
		return Translate(err)
	}
	s.audit.Record(ctx, p, "team.member_upserted", "team", team.String(), map[string]any{"user_id": u.ID, "role": in.Role})
	return nil
}

func (s *TeamService) RemoveMember(ctx context.Context, p *Principal, team, user uuid.UUID) error {
	if err := p.Require(team, model.TeamRoleOwner); err != nil {
		return err
	}
	if err := s.repo.RemoveMember(ctx, team, user); err != nil {
		return Translate(err)
	}
	s.audit.Record(ctx, p, "team.member_removed", "team", team.String(), map[string]any{"user_id": user})
	return nil
}

// Budgets -------------------------------------------------------------------------------

type BudgetInput struct {
	MonthlyLimitNanos *int64 `json:"monthly_limit_nanos"`
	DailyLimitNanos   *int64 `json:"daily_limit_nanos"`
	WarnThresholdPct  *int   `json:"warn_threshold_pct"`
}

func (s *TeamService) Budget(ctx context.Context, p *Principal, team uuid.UUID) (*budget.Status, error) {
	if err := p.Require(team, model.TeamRoleViewer); err != nil {
		return nil, err
	}
	return s.budgets.Status(ctx, team)
}

func (s *TeamService) SetBudget(ctx context.Context, p *Principal, team uuid.UUID, in BudgetInput) (*budget.Status, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	if _, err := s.repo.Team(ctx, team); err != nil {
		return nil, Translate(err)
	}
	for _, v := range []*int64{in.MonthlyLimitNanos, in.DailyLimitNanos} {
		if v != nil && *v < 0 {
			return nil, Invalid("budget limits must be non-negative")
		}
	}
	b := &model.TeamBudget{TeamID: team, MonthlyLimit: in.MonthlyLimitNanos, DailyLimit: in.DailyLimitNanos, WarnPct: 80}
	if in.WarnThresholdPct != nil {
		if *in.WarnThresholdPct < 1 || *in.WarnThresholdPct > 100 {
			return nil, Invalid("warn_threshold_pct must be between 1 and 100")
		}
		b.WarnPct = *in.WarnThresholdPct
	}
	if err := s.repo.UpsertTeamBudget(ctx, b); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "team.budget_updated", "team", team.String(), map[string]any{"monthly_limit_nanos": b.MonthlyLimit, "daily_limit_nanos": b.DailyLimit})
	return s.budgets.Status(ctx, team)
}

// API keys --------------------------------------------------------------------------------

type KeyInput struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

type CreatedKey struct {
	*model.APIKey
	Key string `json:"key"` // raw key, returned exactly once
}

func (s *TeamService) Keys(ctx context.Context, p *Principal, team uuid.UUID) ([]*model.APIKey, error) {
	if err := p.Require(team, model.TeamRoleViewer); err != nil {
		return nil, err
	}
	return s.repo.ListAPIKeys(ctx, team)
}

func (s *TeamService) CreateKey(ctx context.Context, p *Principal, team uuid.UUID, in KeyInput) (*CreatedKey, error) {
	if err := p.Require(team, model.TeamRoleDeveloper); err != nil {
		return nil, err
	}
	t, err := s.repo.Team(ctx, team)
	if err != nil {
		return nil, Translate(err)
	}
	if t.ArchivedAt != nil {
		return nil, Invalid("team is archived")
	}
	name, err := cleanName("name", in.Name, 100)
	if err != nil {
		return nil, err
	}
	raw, prefix, err := security.GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	k := &model.APIKey{ID: model.NewID(), TeamID: team, Name: name, Prefix: prefix, CreatedBy: &p.User.ID}
	if in.ExpiresInDays != nil {
		if *in.ExpiresInDays < 1 || *in.ExpiresInDays > 3650 {
			return nil, Invalid("expires_in_days must be between 1 and 3650")
		}
		exp := time.Now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour)
		k.ExpiresAt = &exp
	}
	if err := s.repo.CreateAPIKey(ctx, k, s.hasher.Hash(raw)); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "api_key.created", "api_key", k.ID.String(), map[string]any{"team_id": team, "name": name, "prefix": prefix})
	return &CreatedKey{APIKey: k, Key: raw}, nil
}

func (s *TeamService) RevokeKey(ctx context.Context, p *Principal, team, key uuid.UUID) error {
	if err := p.Require(team, model.TeamRoleDeveloper); err != nil {
		return err
	}
	if err := s.repo.RevokeAPIKey(ctx, team, key); err != nil {
		return Translate(err)
	}
	s.keys.Invalidate(&key, nil)
	s.audit.Record(ctx, p, "api_key.revoked", "api_key", key.String(), map[string]any{"team_id": team})
	return nil
}
