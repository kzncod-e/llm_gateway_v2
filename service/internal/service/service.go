// Package service contains control-plane use cases: validation, authorization
// and auditing on top of the repository.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"service/internal/model"
	"service/internal/repository"
)

// Error is a user-facing error with an HTTP status.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func Invalid(format string, a ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: fmt.Sprintf(format, a...)}
}

var (
	ErrForbidden    = &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "you do not have permission to perform this action"}
	ErrUnauthorized = &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "authentication required"}
	ErrNotFound     = &Error{Status: http.StatusNotFound, Code: "not_found", Message: "resource not found"}
	ErrConflict     = &Error{Status: http.StatusConflict, Code: "conflict", Message: "a resource with this identifier already exists"}
)

// Translate converts repository errors into service errors.
func Translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repository.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, repository.ErrConflict):
		return ErrConflict
	}
	return err
}

// Principal is an authenticated console user with their team roles.
type Principal struct {
	User  *model.User
	Teams map[uuid.UUID]string // team ID → role
	IP    string
}

func (p *Principal) IsAdmin() bool { return p.User.IsAdmin() }

func (p *Principal) TeamRole(team uuid.UUID) string {
	if p.IsAdmin() {
		return model.TeamRoleOwner
	}
	return p.Teams[team]
}

// Require checks that the principal has at least role in team. Non-members get
// ErrNotFound so team existence is not revealed.
func (p *Principal) Require(team uuid.UUID, role string) error {
	have := model.TeamRoleRank(p.TeamRole(team))
	if have == 0 {
		return ErrNotFound
	}
	if have < model.TeamRoleRank(role) {
		return ErrForbidden
	}
	return nil
}

func (p *Principal) RequireAdmin() error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	return nil
}

// VisibleTeams returns nil (all teams) for admins, else the member team IDs.
func (p *Principal) VisibleTeams() []uuid.UUID {
	if p.IsAdmin() {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(p.Teams))
	for id := range p.Teams {
		ids = append(ids, id)
	}
	return ids
}

// Auditor records administrative actions.
type Auditor struct {
	repo *repository.Repo
	log  *slog.Logger
}

func NewAuditor(repo *repository.Repo, log *slog.Logger) *Auditor {
	return &Auditor{repo: repo, log: log}
}

func (a *Auditor) Record(ctx context.Context, p *Principal, action, targetType, targetID string, meta map[string]any) {
	var actor *uuid.UUID
	ip := ""
	if p != nil {
		actor, ip = &p.User.ID, p.IP
	}
	if err := a.repo.InsertAudit(context.WithoutCancel(ctx), actor, action, targetType, targetID, meta, ip); err != nil {
		a.log.Error("failed to write audit log", "error", err, "action", action)
	}
}

// Validation helpers -------------------------------------------------------------------

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,48}[a-z0-9]$`)

func validSlug(s string) bool { return slugRe.MatchString(s) }

func cleanName(field, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > max {
		return "", Invalid("%s is required (max %d characters)", field, max)
	}
	return s, nil
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && len(s) <= 254
}

func validBaseURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.RawQuery == "" && u.Fragment == ""
}

// Slugify derives a slug from a display name.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	return s
}
