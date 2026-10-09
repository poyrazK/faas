// adr: 856
// Package validatorbundle defines the shared release-owned validator registry.
package validatorbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Bundle struct {
	AppID        string               `json:"app_id"`
	DeploymentID string               `json:"deployment_id"`
	Runtime      api.ExecutionRuntime `json:"runtime"`
	Entrypoint   string               `json:"entrypoint"`
	Files        []api.ExecutionFile  `json:"files"`
	SHA256       string               `json:"sha256"`
}

func Hash(b Bundle) string {
	body, _ := json.Marshal(struct {
		Runtime    api.ExecutionRuntime `json:"runtime"`
		Entrypoint string               `json:"entrypoint"`
		Files      []api.ExecutionFile  `json:"files"`
	}{b.Runtime, b.Entrypoint, b.Files})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func Load(path string) (map[string]Bundle, error) {
	f, err := os.Open(path) //nolint:forbidigo // Operator-configured registry path, never a customer-provided path.
	if err != nil {
		return nil, errors.New("validator bundle registry is unavailable")
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, api.MaxDurableEntityValidatorRegistryBytes+1))
	if err != nil || len(body) > api.MaxDurableEntityValidatorRegistryBytes {
		return nil, errors.New("validator registry exceeds its byte limit")
	}
	var bundles []Bundle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundles) != nil || decoder.Decode(new(any)) != io.EOF || len(bundles) == 0 || len(bundles) > api.MaxDurableEntityValidatorBundles {
		return nil, errors.New("invalid validator registry")
	}
	out := make(map[string]Bundle, len(bundles))
	for _, b := range bundles {
		if err := Validate(b); err != nil {
			return nil, err
		}
		if _, exists := out[b.DeploymentID]; exists {
			return nil, errors.New("duplicate validator deployment")
		}
		out[b.DeploymentID] = b
	}
	return out, nil
}

// Merge preserves an existing deployment binding. Updating its bytes requires a
// new deployment identity; repeated identical release packaging is idempotent.
func Merge(existing map[string]Bundle, bundle Bundle) ([]byte, error) {
	if prior, ok := existing[bundle.DeploymentID]; ok && (prior.AppID != bundle.AppID || prior.SHA256 != bundle.SHA256) {
		return nil, errors.New("deployment already has a different validator bundle")
	}
	if err := Validate(bundle); err != nil {
		return nil, err
	}
	next := make(map[string]Bundle, len(existing)+1)
	for id, b := range existing {
		if id != b.DeploymentID {
			return nil, errors.New("invalid registry key")
		}
		if err := Validate(b); err != nil {
			return nil, err
		}
		next[id] = b
	}
	next[bundle.DeploymentID] = bundle
	if len(next) > api.MaxDurableEntityValidatorBundles {
		return nil, errors.New("validator registry bundle limit exceeded")
	}
	ids := make([]string, 0, len(next))
	for id := range next {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entries := make([]Bundle, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, next[id])
	}
	body, err := json.Marshal(entries)
	if err != nil || len(body) > api.MaxDurableEntityValidatorRegistryBytes {
		return nil, errors.New("validator registry byte limit exceeded")
	}
	return body, nil
}

// Validate rejects malformed identities, unsupported runtimes and altered bytes.
func Validate(b Bundle) error {
	app, e1 := uuid.Parse(b.AppID)
	deployment, e2 := uuid.Parse(b.DeploymentID)
	if e1 != nil || e2 != nil || app == uuid.Nil || deployment == uuid.Nil || app.String() != b.AppID || deployment.String() != b.DeploymentID || !b.Runtime.Valid() || b.Entrypoint == "" || len(b.Files) == 0 || b.SHA256 != Hash(b) || api.ValidateExecutionBundle(b.Entrypoint, b.Files, api.MaxDurableEntityValidatorRegistryBytes) != nil {
		return errors.New("invalid validator bundle identity or integrity")
	}
	return nil
}
