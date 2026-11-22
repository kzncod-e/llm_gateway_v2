package repository

import (
	"context"
	"fmt"
	"time"

	"service/internal/gateway"
	"service/internal/pricing"
)

// Decrypter opens a provider's stored credential.
type Decrypter func(p ProviderCredential) (string, error)

type ProviderCredential struct {
	ID         string
	Ciphertext []byte
}

// LoadCatalog reads routing configuration and builds a gateway snapshot.
// Providers whose credential cannot be decrypted are loaded disabled.
func (r *Repo) LoadCatalog(ctx context.Context, decrypt Decrypter) (*gateway.Snapshot, []error, error) {
	var d gateway.SnapshotData
	var warnings []error

	provs, err := r.ListProviders(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range provs {
		e := gateway.ProviderEntry{ID: p.ID, Slug: p.Slug, Name: p.Name, Type: p.Type, BaseURL: p.BaseURL, Enabled: p.Enabled,
			Priority: p.Priority, Timeout: time.Duration(p.TimeoutMs) * time.Millisecond}
		if p.HasAPIKey {
			key, err := decrypt(ProviderCredential{ID: p.ID.String(), Ciphertext: p.APIKeyCiphertext})
			if err != nil {
				warnings = append(warnings, fmt.Errorf("provider %s: %w", p.Slug, err))
				e.Enabled = false
			}
			e.APIKey = key
		}
		d.Providers = append(d.Providers, e)
	}

	rows, err := r.db.Query(ctx, `SELECT id, provider_id, name, display_name, enabled FROM models`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var m gateway.ModelRow
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Name, &m.DisplayName, &m.Enabled); err != nil {
			rows.Close()
			return nil, nil, err
		}
		d.Models = append(d.Models, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows, err = r.db.Query(ctx, `SELECT id, model_id, effective_from, currency, input_per_mtok_nanos, cached_input_per_mtok_nanos,
		cache_write_per_mtok_nanos, output_per_mtok_nanos FROM model_pricing`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var p pricing.Price
		if err := rows.Scan(&p.ID, &p.ModelID, &p.EffectiveFrom, &p.Currency, &p.InputPerM, &p.CachedInputPerM, &p.CacheWritePerM, &p.OutputPerM); err != nil {
			rows.Close()
			return nil, nil, err
		}
		d.Prices = append(d.Prices, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	pols, err := r.ListPolicies(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pols {
		d.Policies = append(d.Policies, gateway.PolicyRow{ID: p.ID, Alias: p.Alias, Enabled: p.Enabled, MaxAttemptsPerTarget: p.MaxAttemptsPerTarget,
			RetryBaseMs: p.RetryBaseMs, RetryMaxMs: p.RetryMaxMs, TargetModelIDs: p.TargetModelIDs})
	}
	return gateway.BuildSnapshot(d, time.Now()), warnings, nil
}
