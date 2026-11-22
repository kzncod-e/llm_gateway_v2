// Package config loads and validates service configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env         string
	HTTPAddr    string
	LogLevel    string
	DatabaseURL string
	RedisURL    string

	EncryptionKey []byte // 32 bytes, AES-256
	APIKeyPepper  []byte

	AdminEmail    string
	AdminPassword string
	SeedDemo      bool
	MockBaseURL   string
	DemoAPIKey    string

	CookieSecure  bool
	SessionTTL    time.Duration
	AutoMigrate   bool
	TrustedProxies []string

	RateLimitFailOpen     bool
	DefaultTeamRPM        int
	DefaultTeamConcurrency int

	DefaultMaxOutputTokens int
	RequestTimeout         time.Duration
	StreamTimeout          time.Duration
	ReservationTTL         time.Duration
	CatalogRefresh         time.Duration

	BreakerFailureThreshold int
	BreakerOpenDuration     time.Duration

	OTLPEndpoint string // host:port, empty disables tracing
	ServiceName  string
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	var errs []error
	l := loader{errs: &errs}

	c := &Config{
		Env:         l.str("APP_ENV", "development"),
		HTTPAddr:    l.str("HTTP_ADDR", ":8080"),
		LogLevel:    l.str("LOG_LEVEL", "info"),
		DatabaseURL: l.required("DATABASE_URL"),
		RedisURL:    l.str("REDIS_URL", "redis://localhost:6379/0"),

		AdminEmail:    strings.ToLower(strings.TrimSpace(l.str("ADMIN_EMAIL", ""))),
		AdminPassword: l.str("ADMIN_PASSWORD", ""),
		SeedDemo:      l.boolean("SEED_DEMO", false),
		MockBaseURL:   l.str("MOCK_PROVIDER_URL", "http://localhost:8090"),
		DemoAPIKey:    l.str("DEMO_API_KEY", ""),

		CookieSecure: l.boolean("COOKIE_SECURE", false),
		SessionTTL:   l.duration("SESSION_TTL", 7*24*time.Hour),
		AutoMigrate:  l.boolean("AUTO_MIGRATE", true),

		RateLimitFailOpen:      l.boolean("RATE_LIMIT_FAIL_OPEN", false),
		DefaultTeamRPM:         l.integer("DEFAULT_TEAM_RPM", 600),
		DefaultTeamConcurrency: l.integer("DEFAULT_TEAM_CONCURRENCY", 50),

		DefaultMaxOutputTokens: l.integer("DEFAULT_MAX_OUTPUT_TOKENS", 4096),
		RequestTimeout:         l.duration("REQUEST_TIMEOUT", 120*time.Second),
		StreamTimeout:          l.duration("STREAM_TIMEOUT", 10*time.Minute),
		ReservationTTL:         l.duration("RESERVATION_TTL", 15*time.Minute),
		CatalogRefresh:         l.duration("CATALOG_REFRESH", 30*time.Second),

		BreakerFailureThreshold: l.integer("BREAKER_FAILURE_THRESHOLD", 5),
		BreakerOpenDuration:     l.duration("BREAKER_OPEN_DURATION", 30*time.Second),

		OTLPEndpoint: l.str("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		ServiceName:  l.str("OTEL_SERVICE_NAME", "llm-gateway"),
	}
	if tp := l.str("TRUSTED_PROXIES", ""); tp != "" {
		for _, p := range strings.Split(tp, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.TrustedProxies = append(c.TrustedProxies, p)
			}
		}
	}

	c.EncryptionKey = l.base64Key("ENCRYPTION_KEY", 32)
	pepper := l.required("API_KEY_PEPPER")
	if pepper != "" && len(pepper) < 32 {
		errs = append(errs, errors.New("API_KEY_PEPPER must be at least 32 characters"))
	}
	c.APIKeyPepper = []byte(pepper)

	if c.AdminEmail != "" && len(c.AdminPassword) < 12 {
		errs = append(errs, errors.New("ADMIN_PASSWORD must be at least 12 characters when ADMIN_EMAIL is set"))
	}
	if c.ReservationTTL <= c.StreamTimeout {
		errs = append(errs, errors.New("RESERVATION_TTL must be longer than STREAM_TIMEOUT"))
	}
	if c.DefaultTeamRPM <= 0 || c.DefaultTeamConcurrency <= 0 || c.DefaultMaxOutputTokens <= 0 || c.BreakerFailureThreshold <= 0 {
		errs = append(errs, errors.New("limits and thresholds must be positive"))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return c, nil
}

func (c *Config) IsProduction() bool { return c.Env == "production" }

type loader struct{ errs *[]error }

func (l loader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func (l loader) required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		*l.errs = append(*l.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (l loader) boolean(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*l.errs = append(*l.errs, fmt.Errorf("%s: invalid boolean %q", key, v))
		return def
	}
	return b
}

func (l loader) integer(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*l.errs = append(*l.errs, fmt.Errorf("%s: invalid integer %q", key, v))
		return def
	}
	return n
}

func (l loader) duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		*l.errs = append(*l.errs, fmt.Errorf("%s: invalid duration %q", key, v))
		return def
	}
	return d
}

func (l loader) base64Key(key string, size int) []byte {
	v := l.required(key)
	if v == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != size {
		*l.errs = append(*l.errs, fmt.Errorf("%s must be %d bytes, base64-encoded (generate with: openssl rand -base64 %d)", key, size, size))
		return nil
	}
	return b
}
