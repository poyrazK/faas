package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

// SidecarEnvReseal is the runtime-domain copy of one sidecar env value
// (ADR 20261022111812). deployments.sidecars stays as apid wrote it: guard
// triggers keep it immutable and readiness and promotion fingerprints hash
// it. `gregalectl seal-domain migrate --domain runtime` writes the copy
// instead, and schedd sends it to vmmd while the deployment still declares
// the ciphertext whose digest is SourceSHA256.
type SidecarEnvReseal struct {
	DeploymentID string
	SidecarName  string
	Key          string
	SourceSHA256 string
	Kid          string
	Ciphertext   []byte
}

// DeploymentSidecars is a deployment whose sidecars declare env values,
// as the runtime re-seal lists them.
type DeploymentSidecars struct {
	ID       string
	Sidecars json.RawMessage
}

// SidecarEnvSourceDigest is the SourceSHA256 of a declared ciphertext.
func SidecarEnvSourceDigest(ciphertext []byte) string {
	sum := sha256.Sum256(ciphertext)
	return hex.EncodeToString(sum[:])
}

func sidecarEnvResealKey(deploymentID, sidecarName, key string) string {
	return deploymentID + "\x00" + sidecarName + "\x00" + key
}

func validSidecarEnvReseal(r SidecarEnvReseal) bool {
	return r.DeploymentID != "" && r.SidecarName != "" && r.Key != "" && len(r.SourceSHA256) == sha256.Size*2 && r.Kid != "" && len(r.Ciphertext) > 0
}

// ListDeploymentSidecarEnvReseals mirrors PgStore.
func (m *MemStore) ListDeploymentSidecarEnvReseals(_ context.Context, deploymentID string) ([]SidecarEnvReseal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SidecarEnvReseal{}
	for _, r := range m.sidecarEnvReseals {
		if r.DeploymentID == deploymentID {
			r.Ciphertext = bytes.Clone(r.Ciphertext)
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SidecarName != out[j].SidecarName {
			return out[i].SidecarName < out[j].SidecarName
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// UpsertSidecarEnvReseal mirrors PgStore: it writes r when no copy is
// stored, or when the stored copy is still previous, and reports whether it
// did.
func (m *MemStore) UpsertSidecarEnvReseal(_ context.Context, r SidecarEnvReseal, previous []byte) (bool, error) {
	if !validSidecarEnvReseal(r) {
		return false, errors.New("state: sidecar env reseal needs a deployment, sidecar, key, source digest, key id and ciphertext")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[r.DeploymentID]; !ok {
		return false, nil // the deployment was deleted since it was listed
	}
	key := sidecarEnvResealKey(r.DeploymentID, r.SidecarName, r.Key)
	held, ok := m.sidecarEnvReseals[key]
	if ok && !bytes.Equal(held.Ciphertext, previous) {
		return false, nil
	}
	if m.sidecarEnvReseals == nil {
		m.sidecarEnvReseals = map[string]SidecarEnvReseal{}
	}
	r.Ciphertext = bytes.Clone(r.Ciphertext)
	m.sidecarEnvReseals[key] = r
	return true, nil
}
