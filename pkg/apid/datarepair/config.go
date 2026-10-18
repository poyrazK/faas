package datarepair

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/operations"
)

// LoadBindings accepts only operator-owned configuration. Secrets are resolved
// by environment-variable name and are never serialized into Operation records.
func LoadBindings(path string, getenv func(string) string) ([]Binding, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:forbidigo // Operator-owned daemon configuration; never a customer-supplied path.
	if err != nil {
		return nil, core.ErrInvalid
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, int64(api.DataRepairMaxRequestBytes+1)))
	if err != nil || len(raw) > api.DataRepairMaxRequestBytes {
		return nil, core.ErrInvalid
	}
	raw, err = operations.CanonicalJSON(raw)
	if err != nil {
		return nil, core.ErrInvalid
	}
	var cfg struct {
		Version  int `json:"version"`
		Bindings []struct {
			Scope              core.Scope `json:"scope"`
			Generation         string     `json:"generation"`
			URL                string     `json:"url"`
			TokenEnv           string     `json:"token_env"`
			ScheduledRehearsal bool       `json:"scheduled_rehearsal"`
		} `json:"bindings"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil || cfg.Version != 1 || len(cfg.Bindings) == 0 {
		return nil, core.ErrInvalid
	}
	seen := map[core.Scope]bool{}
	out := []Binding{}
	for _, b := range cfg.Bindings {
		if b.Scope.Validate() != nil || seen[b.Scope] || !validUUID(b.Generation) || b.TokenEnv == "" {
			return nil, core.ErrInvalid
		}
		seen[b.Scope] = true
		executor, err := NewRemote(b.URL, getenv(b.TokenEnv))
		if err != nil {
			return nil, core.ErrInvalid
		}
		origin := b.URL
		if b.ScheduledRehearsal {
			origin += "#scheduled-rehearsal-v1"
		}
		out = append(out, Binding{Scope: b.Scope, Identity: BindingIdentity(b.Scope, b.Generation, origin), Executor: executor, ScheduledRehearsal: b.ScheduledRehearsal})
	}
	return out, nil
}
