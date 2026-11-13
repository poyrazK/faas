package secretd

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// SidecarEnvNamespace is the secretbox namespace apid seals sidecar direct
// env values with (pkg/apid/apidserver handlers_sidecars.go).
const SidecarEnvNamespace = "sidecar_env"

// ErrUnknownSidecar means the deployment does not belong to the app or
// declares no sidecar of that name.
var ErrUnknownSidecar = errors.New("secretd: the app's deployment declares no such sidecar")

// SidecarEnvReseal is a runtime-domain copy of one sidecar env value
// (state.SidecarEnvReseal, ADR 20261022111812): it replaces the declared
// ciphertext only while the deployment still declares the value it copied.
type SidecarEnvReseal struct {
	SidecarName  string
	Key          string
	SourceSHA256 string
	Ciphertext   []byte
}

// SidecarCiphertexts returns sidecarName's sealed env values from a
// deployment's persisted sidecars (deployments.sidecars: a JSON array whose
// env values are base64 ciphertext), with the matching re-seals applied the
// way schedd applies them.
func SidecarCiphertexts(sidecarsJSON []byte, sidecarName string, reseals []SidecarEnvReseal) (map[string][]byte, error) {
	if len(sidecarsJSON) == 0 || sidecarName == "" {
		return nil, ErrUnknownSidecar
	}
	var sidecars []struct {
		Name string            `json:"name"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal(sidecarsJSON, &sidecars); err != nil {
		return nil, fmt.Errorf("secretd: deployment sidecars: %w", err)
	}
	for _, sc := range sidecars {
		if sc.Name != sidecarName {
			continue
		}
		out := make(map[string][]byte, len(sc.Env))
		for key, encoded := range sc.Env {
			ct, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, fmt.Errorf("secretd: sidecar env %q: %w", key, err)
			}
			out[key] = ct
		}
		for _, r := range reseals {
			if r.SidecarName != sidecarName {
				continue
			}
			declared, ok := out[r.Key]
			if !ok {
				continue
			}
			sum := sha256.Sum256(declared)
			if hex.EncodeToString(sum[:]) == r.SourceSHA256 {
				out[r.Key] = r.Ciphertext
			}
		}
		return out, nil
	}
	return nil, ErrUnknownSidecar
}
