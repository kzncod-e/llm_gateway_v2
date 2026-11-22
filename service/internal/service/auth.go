package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"service/internal/model"
	"service/internal/repository"
	"service/internal/security"
)

// RateChecker limits login attempts.
type RateChecker interface {
	AllowRate(ctx context.Context, key string, perMinute int) error
}

type AuthService struct {
	repo    *repository.Repo
	audit   *Auditor
	limiter RateChecker
	ttl     time.Duration
	log     *slog.Logger
}

func NewAuthService(repo *repository.Repo, audit *Auditor, limiter RateChecker, ttl time.Duration, log *slog.Logger) *AuthService {
	return &AuthService{repo: repo, audit: audit, limiter: limiter, ttl: ttl, log: log}
}

var errBadCredentials = &Error{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "invalid email or password"}

// Login verifies credentials and creates a session. It returns the raw token.
func (s *AuthService) Login(ctx context.Context, email, password, ua, ip string) (string, *model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if s.limiter != nil {
		// Keyed by IP and account: behind the console proxy all users share one IP.
		if err := s.limiter.AllowRate(ctx, "login:"+ip+":"+email, 10); err != nil {
			return "", nil, &Error{Status: http.StatusTooManyRequests, Code: "too_many_attempts", Message: "too many login attempts, try again in a minute"}
		}
	}
	u, err := s.repo.UserByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		security.EqualizeTiming(password)
		return "", nil, errBadCredentials
	}
	if err != nil {
		return "", nil, err
	}
	if !security.CheckPassword(u.PasswordHash, password) || u.DisabledAt != nil {
		return "", nil, errBadCredentials
	}
	token, err := security.NewSessionToken()
	if err != nil {
		return "", nil, err
	}
	if err := s.repo.CreateSession(ctx, u.ID, security.HashToken(token), ua, ip, time.Now().Add(s.ttl)); err != nil {
		return "", nil, err
	}
	if err := s.repo.TouchLogin(ctx, u.ID); err != nil {
		s.log.Warn("touch login", "error", err)
	}
	s.audit.Record(ctx, &Principal{User: u, IP: ip}, "auth.login", "user", u.ID.String(), nil)
	return token, u, nil
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	return s.repo.DeleteSession(ctx, security.HashToken(token))
}

// Authenticate resolves a session token to a principal.
func (s *AuthService) Authenticate(ctx context.Context, token, ip string) (*Principal, error) {
	if token == "" || len(token) > 100 {
		return nil, ErrUnauthorized
	}
	u, err := s.repo.SessionUser(ctx, security.HashToken(token))
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	roles, err := s.repo.MemberRoles(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &Principal{User: u, Teams: roles, IP: ip}, nil
}

func (s *AuthService) ChangePassword(ctx context.Context, p *Principal, current, next string) error {
	if !security.CheckPassword(p.User.PasswordHash, current) {
		return &Error{Status: http.StatusBadRequest, Code: "invalid_credentials", Message: "current password is incorrect"}
	}
	hash, err := security.HashPassword(next)
	if err != nil {
		return Invalid("%s", err.Error())
	}
	if err := s.repo.SetPassword(ctx, p.User.ID, hash); err != nil {
		return err
	}
	s.audit.Record(ctx, p, "auth.password_changed", "user", p.User.ID.String(), nil)
	return nil
}

// BootstrapAdmin creates the first administrator when no users exist.
func (s *AuthService) BootstrapAdmin(ctx context.Context, email, password string) (bool, error) {
	if email == "" {
		return false, nil
	}
	n, err := s.repo.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return false, err
	}
	u := &model.User{ID: model.NewID(), Email: email, Name: "Administrator", Role: model.RoleAdmin, PasswordHash: hash}
	if err := s.repo.CreateUser(ctx, u); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return false, nil // another instance won the race
		}
		return false, err
	}
	s.audit.Record(ctx, nil, "user.bootstrap_admin", "user", u.ID.String(), map[string]any{"email": email})
	return true, nil
}

// Users (admin) ---------------------------------------------------------------------

type CreateUserInput struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *AuthService) CreateUser(ctx context.Context, p *Principal, in CreateUserInput) (*model.User, error) {
	if err := p.RequireAdmin(); err != nil {
		return nil, err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(in.Email) {
		return nil, Invalid("a valid email is required")
	}
	if in.Role == "" {
		in.Role = model.RoleMember
	}
	if in.Role != model.RoleAdmin && in.Role != model.RoleMember {
		return nil, Invalid("role must be admin or member")
	}
	name, err := cleanName("name", in.Name, 100)
	if err != nil {
		return nil, err
	}
	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return nil, Invalid("%s", err.Error())
	}
	u := &model.User{ID: model.NewID(), Email: in.Email, Name: name, Role: in.Role, PasswordHash: hash}
	if err := s.repo.CreateUser(ctx, u); err != nil {
		return nil, Translate(err)
	}
	s.audit.Record(ctx, p, "user.created", "user", u.ID.String(), map[string]any{"email": u.Email, "role": u.Role})
	return u, nil
}

type UpdateUserInput struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

func (s *AuthService) UpdateUser(ctx context.Context, p *Principal, id uuid.UUID, in UpdateUserInput) error {
	if err := p.RequireAdmin(); err != nil {
		return err
	}
	if in.Role != nil && *in.Role != model.RoleAdmin && *in.Role != model.RoleMember {
		return Invalid("role must be admin or member")
	}
	if id == p.User.ID && ((in.Role != nil && *in.Role != model.RoleAdmin) || (in.Disabled != nil && *in.Disabled)) {
		return Invalid("you cannot demote or disable your own account")
	}
	if in.Name != nil {
		n, err := cleanName("name", *in.Name, 100)
		if err != nil {
			return err
		}
		in.Name = &n
	}
	if err := s.repo.UpdateUser(ctx, id, repository.UserUpdate{Name: in.Name, Role: in.Role, Disabled: in.Disabled}); err != nil {
		return Translate(err)
	}
	if in.Disabled != nil && *in.Disabled {
		if err := s.repo.DeleteUserSessions(ctx, id); err != nil {
			return err
		}
	}
	s.audit.Record(ctx, p, "user.updated", "user", id.String(), map[string]any{"role": in.Role, "disabled": in.Disabled})
	return nil
}

// UpdateProfile lets any user change their display name.
func (s *AuthService) UpdateProfile(ctx context.Context, p *Principal, name string) error {
	n, err := cleanName("name", name, 100)
	if err != nil {
		return err
	}
	return Translate(s.repo.UpdateUser(ctx, p.User.ID, repository.UserUpdate{Name: &n}))
}

// KeyAuth authenticates gateway API keys with a short-lived in-memory cache.
type KeyAuth struct {
	repo   *repository.Repo
	hasher *security.KeyHasher
	ttl    time.Duration
	log    *slog.Logger

	mu      sync.Mutex
	cache   map[string]cachedKey
	touched map[uuid.UUID]time.Time
}

type cachedKey struct {
	p   *repository.KeyPrincipal
	err error
	exp time.Time
}

func NewKeyAuth(repo *repository.Repo, hasher *security.KeyHasher, log *slog.Logger) *KeyAuth {
	return &KeyAuth{repo: repo, hasher: hasher, ttl: 30 * time.Second, log: log, cache: map[string]cachedKey{}, touched: map[uuid.UUID]time.Time{}}
}

var ErrInvalidKey = errors.New("invalid api key")

// Authenticate returns the key and team for a raw key, or ErrInvalidKey.
func (a *KeyAuth) Authenticate(ctx context.Context, raw string) (*repository.KeyPrincipal, error) {
	if !security.LooksLikeAPIKey(raw) {
		return nil, ErrInvalidKey
	}
	h := string(a.hasher.Hash(raw))
	now := time.Now()
	a.mu.Lock()
	c, ok := a.cache[h]
	a.mu.Unlock()
	if !ok || now.After(c.exp) {
		p, err := a.repo.KeyByHash(ctx, []byte(h))
		if errors.Is(err, repository.ErrNotFound) {
			err = ErrInvalidKey
		} else if err != nil {
			return nil, err // database error: do not cache
		}
		c = cachedKey{p: p, err: err, exp: now.Add(a.ttl)}
		a.mu.Lock()
		if len(a.cache) > 10000 {
			a.cache = map[string]cachedKey{}
		}
		a.cache[h] = c
		a.mu.Unlock()
	}
	if c.err != nil {
		return nil, c.err
	}
	if !c.p.Key.Active(now) || c.p.Team.ArchivedAt != nil {
		return nil, ErrInvalidKey
	}
	a.touch(c.p.Key.ID, now)
	return c.p, nil
}

// touch updates last_used_at at most once per minute per key, asynchronously.
func (a *KeyAuth) touch(id uuid.UUID, now time.Time) {
	a.mu.Lock()
	last := a.touched[id]
	if now.Sub(last) < time.Minute {
		a.mu.Unlock()
		return
	}
	a.touched[id] = now
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.repo.TouchAPIKey(ctx, id, now); err != nil {
			a.log.Warn("touch api key", "error", err)
		}
	}()
}

// Invalidate drops cached entries for a key (after revocation) or a team.
func (a *KeyAuth) Invalidate(keyID, teamID *uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for h, c := range a.cache {
		if c.p == nil {
			continue
		}
		if (keyID != nil && c.p.Key.ID == *keyID) || (teamID != nil && c.p.Team.ID == *teamID) {
			delete(a.cache, h)
		}
	}
}
