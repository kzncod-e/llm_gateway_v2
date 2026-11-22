// Package model holds domain entities shared across layers. JSON tags define
// how entities are exposed by the control-plane API; secret fields are never serialized.
package model

import (
	"time"

	"github.com/google/uuid"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"

	TeamRoleOwner     = "owner"
	TeamRoleDeveloper = "developer"
	TeamRoleViewer    = "viewer"
)

type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	PasswordHash string     `json:"-"`
	DisabledAt   *time.Time `json:"disabled_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

type Team struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	Description      string     `json:"description"`
	RPMLimit         *int       `json:"rpm_limit"`
	ConcurrencyLimit *int       `json:"concurrency_limit"`
	ArchivedAt       *time.Time `json:"archived_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type TeamMember struct {
	TeamID    uuid.UUID `json:"team_id"`
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// TeamRoleRank orders team roles so that permission checks can use >=.
func TeamRoleRank(role string) int {
	switch role {
	case TeamRoleOwner:
		return 3
	case TeamRoleDeveloper:
		return 2
	case TeamRoleViewer:
		return 1
	default:
		return 0
	}
}

type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	TeamID     uuid.UUID  `json:"team_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedBy  *uuid.UUID `json:"created_by"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (k *APIKey) Active(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || k.ExpiresAt.After(now))
}

type Provider struct {
	ID               uuid.UUID `json:"id"`
	Slug             string    `json:"slug"`
	Name             string    `json:"name"`
	Type             string    `json:"type"`
	BaseURL          string    `json:"base_url"`
	HasAPIKey        bool      `json:"has_api_key"`
	APIKeyLast4      string    `json:"api_key_last4"`
	KeyVersion       int       `json:"key_version"`
	Enabled          bool      `json:"enabled"`
	Priority         int       `json:"priority"`
	TimeoutMs        int       `json:"timeout_ms"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	APIKeyCiphertext []byte    `json:"-"`
}

var ProviderTypes = []string{"openai", "anthropic", "gemini"}

type Model struct {
	ID           uuid.UUID `json:"id"`
	ProviderID   uuid.UUID `json:"provider_id"`
	ProviderSlug string    `json:"provider_slug"`
	Name         string    `json:"name"`
	DisplayName  string    `json:"display_name"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// CurrentPrice is filled by list endpoints for convenience.
	CurrentPrice *ModelPrice `json:"current_price,omitempty"`
}

type ModelPrice struct {
	ID              uuid.UUID `json:"id"`
	ModelID         uuid.UUID `json:"model_id"`
	EffectiveFrom   time.Time `json:"effective_from"`
	Currency        string    `json:"currency"`
	InputPerM       int64     `json:"input_per_mtok_nanos"`
	CachedInputPerM int64     `json:"cached_input_per_mtok_nanos"`
	CacheWritePerM  int64     `json:"cache_write_per_mtok_nanos"`
	OutputPerM      int64     `json:"output_per_mtok_nanos"`
	CreatedAt       time.Time `json:"created_at"`
}

type RoutingPolicy struct {
	ID                   uuid.UUID   `json:"id"`
	Alias                string      `json:"alias"`
	Description          string      `json:"description"`
	MaxAttemptsPerTarget int         `json:"max_attempts_per_target"`
	RetryBaseMs          int         `json:"retry_base_ms"`
	RetryMaxMs           int         `json:"retry_max_ms"`
	Enabled              bool        `json:"enabled"`
	TargetModelIDs       []uuid.UUID `json:"target_model_ids"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
}

type TeamBudget struct {
	TeamID       uuid.UUID `json:"team_id"`
	MonthlyLimit *int64    `json:"monthly_limit_nanos"`
	DailyLimit   *int64    `json:"daily_limit_nanos"`
	WarnPct      int       `json:"warn_threshold_pct"`
}

type AuditLog struct {
	ID          int64          `json:"id"`
	ActorUserID *uuid.UUID     `json:"actor_user_id"`
	ActorEmail  string         `json:"actor_email"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	Metadata    map[string]any `json:"metadata"`
	IP          string         `json:"ip"`
	CreatedAt   time.Time      `json:"created_at"`
}

// NewID returns a time-ordered UUID v7.
func NewID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
