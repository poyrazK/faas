// adr: 590
package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// A full clone carries the source environment's shared secrets
// (ADR-20261104120000 amendment 2026-11-21). A secret is captured as its
// sealed envelope: the envelope is not bound to an environment, so the stage
// receives the same ciphertext under the same key ID, as a clone without a
// checkpoint already does (CopyProjectEnvironmentSecrets). No value is opened.
type projectCloneSharedSecrets struct {
	Secrets      []projectCloneSharedSecret      `json:"secrets"`
	Refs         []projectCloneSecretRef         `json:"refs"`
	Suppressions []projectCloneSecretSuppression `json:"suppressions"`
}

type projectCloneSharedSecret struct {
	Key           string `json:"key"`
	Ciphertext    []byte `json:"ciphertext"`
	Kid           string `json:"kid"`
	ValueHash     string `json:"value_hash"`
	SecretVersion int64  `json:"secret_version"`
}

// projectCloneSecretRef binds one app's variable to a shared secret.
type projectCloneSecretRef struct {
	AppID      string `json:"app_id"`
	Key        string `json:"key"`
	SecretName string `json:"secret_name"`
}

// projectCloneSecretSuppression keeps one shared secret out of an app.
type projectCloneSecretSuppression struct {
	AppID string `json:"app_id"`
	Key   string `json:"key"`
}

func normalizeCloneSharedSecrets(in projectCloneSharedSecrets) (projectCloneSharedSecrets, error) {
	out := projectCloneSharedSecrets{
		Secrets:      make([]projectCloneSharedSecret, 0, len(in.Secrets)),
		Refs:         make([]projectCloneSecretRef, 0, len(in.Refs)),
		Suppressions: make([]projectCloneSecretSuppression, 0, len(in.Suppressions)),
	}
	for _, s := range in.Secrets {
		if api.ValidateEnvKey(s.Key) != nil || len(s.Ciphertext) == 0 || s.Kid == "" || s.ValueHash == "" || s.SecretVersion < 1 {
			return projectCloneSharedSecrets{}, ErrConflict
		}
		s.Ciphertext = bytes.Clone(s.Ciphertext)
		out.Secrets = append(out.Secrets, s)
	}
	for _, r := range in.Refs {
		r.AppID = canonicalMemUUID(r.AppID)
		if r.AppID == "" || api.ValidateEnvKey(r.Key) != nil || r.SecretName == "" {
			return projectCloneSharedSecrets{}, ErrConflict
		}
		out.Refs = append(out.Refs, r)
	}
	for _, s := range in.Suppressions {
		s.AppID = canonicalMemUUID(s.AppID)
		if s.AppID == "" || api.ValidateEnvKey(s.Key) != nil {
			return projectCloneSharedSecrets{}, ErrConflict
		}
		out.Suppressions = append(out.Suppressions, s)
	}
	sort.Slice(out.Secrets, func(i, j int) bool { return out.Secrets[i].Key < out.Secrets[j].Key })
	sort.Slice(out.Refs, func(i, j int) bool {
		a, b := out.Refs[i], out.Refs[j]
		return a.AppID < b.AppID || a.AppID == b.AppID && a.Key < b.Key
	})
	sort.Slice(out.Suppressions, func(i, j int) bool {
		a, b := out.Suppressions[i], out.Suppressions[j]
		return a.AppID < b.AppID || a.AppID == b.AppID && a.Key < b.Key
	})
	for i := 1; i < len(out.Secrets); i++ {
		if out.Secrets[i].Key == out.Secrets[i-1].Key {
			return projectCloneSharedSecrets{}, ErrConflict
		}
	}
	for i := 1; i < len(out.Refs); i++ {
		if out.Refs[i].AppID == out.Refs[i-1].AppID && out.Refs[i].Key == out.Refs[i-1].Key {
			return projectCloneSharedSecrets{}, ErrConflict
		}
	}
	for i := 1; i < len(out.Suppressions); i++ {
		if out.Suppressions[i] == out.Suppressions[i-1] {
			return projectCloneSharedSecrets{}, ErrConflict
		}
	}
	return out, nil
}

// cloneSharedSecretsHash identifies a capture; nil is the capture of a
// release that did not carry shared secrets.
func cloneSharedSecretsHash(in *projectCloneSharedSecrets) string {
	if in == nil {
		return ""
	}
	normalized, err := normalizeCloneSharedSecrets(*in)
	if err != nil {
		return "invalid"
	}
	raw, _ := json.Marshal(normalized)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// forApps keeps the references and suppressions of the cloned apps: the
// capture reads the whole project, the stage holds only the cloned apps.
func (in projectCloneSharedSecrets) forApps(apps map[string]bool) projectCloneSharedSecrets {
	out := projectCloneSharedSecrets{Secrets: in.Secrets, Refs: []projectCloneSecretRef{}, Suppressions: []projectCloneSecretSuppression{}}
	for _, r := range in.Refs {
		if apps[canonicalMemUUID(r.AppID)] {
			out.Refs = append(out.Refs, r)
		}
	}
	for _, s := range in.Suppressions {
		if apps[canonicalMemUUID(s.AppID)] {
			out.Suppressions = append(out.Suppressions, s)
		}
	}
	return out
}

// capturedCloneSharedSecrets is the shared secrets a clone's records carry,
// narrowed to its apps; nil when the capture predates them.
func capturedCloneSharedSecrets(records []projectCloneWorkloadRecord) (*projectCloneSharedSecrets, error) {
	config, err := capturedCloneProjectConfig(records)
	if err != nil || config.SharedSecrets == nil {
		return nil, err
	}
	apps := make(map[string]bool, len(records))
	for _, record := range records {
		apps[canonicalMemUUID(record.AppID)] = true
	}
	scoped := config.SharedSecrets.forApps(apps)
	return &scoped, nil
}

// validateCloneSharedSecretsProof requires the target to hold exactly the
// captured shared secrets, references and suppressions.
func validateCloneSharedSecretsProof(captured *projectCloneSharedSecrets, target projectCloneSharedSecrets) error {
	if captured == nil {
		return nil
	}
	if cloneSharedSecretsHash(captured) != cloneSharedSecretsHash(&target) {
		return ErrConflict
	}
	return nil
}
