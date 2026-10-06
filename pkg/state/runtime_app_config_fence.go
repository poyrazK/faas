package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeAppConfiguration describes the deployed inputs read atomically with
// runtime values. Desired revision heads and delivery observations are excluded.
type RuntimeAppConfiguration struct {
	WorkloadSpecID, SettingsHash, GuestSettingsHash, ArtifactHash, LayersHash string
}

// RuntimeAppConfigFence also covers plaintext values and the immutable deployed
// configuration. Only fingerprints, never plaintext, are stored with a VM.
type RuntimeAppConfigFence struct {
	SecretFence RuntimeAppSecretFence
	Fingerprint string
}

func validRuntimeAppConfigFence(f RuntimeAppConfigFence) bool {
	return !f.SecretFence.empty() && validRuntimeAppSecretFence(f.SecretFence) && validSecretRevision(f.Fingerprint)
}

func NewRuntimeAppConfigFence(snapshot RuntimeAppValuesSnapshot) (RuntimeAppConfigFence, error) {
	secret, err := NewRuntimeAppSecretFence(snapshot)
	if err != nil || !validSecretRevision(snapshot.Configuration.SettingsHash) ||
		!validSecretRevision(snapshot.Configuration.GuestSettingsHash) || !validSecretRevision(snapshot.Configuration.ArtifactHash) ||
		!validSecretRevision(snapshot.Configuration.LayersHash) {
		return RuntimeAppConfigFence{}, ErrConflict
	}
	type variable struct {
		Key, Value string
		CreatedAt  time.Time
	}
	variables := make([]variable, 0, len(snapshot.Values))
	keys := map[string]bool{}
	for _, row := range snapshot.Values {
		if row.AccountID != snapshot.AccountID || row.AppID != snapshot.AppID || row.Scope != snapshot.Scope ||
			api.ValidateEnvKey(row.Key) != nil || keys[row.Key] {
			return RuntimeAppConfigFence{}, ErrConflict
		}
		keys[row.Key] = true
		variables = append(variables, variable{row.Key, row.Value, row.CreatedAt.UTC()})
	}
	sort.Slice(variables, func(i, j int) bool { return variables[i].Key < variables[j].Key })
	hash, err := runtimeConfigurationHash(struct {
		Secret        RuntimeAppSecretFence
		Configuration RuntimeAppConfiguration
		Variables     []variable
	}{secret, snapshot.Configuration, variables})
	if err != nil {
		return RuntimeAppConfigFence{}, err
	}
	return RuntimeAppConfigFence{SecretFence: secret, Fingerprint: hash}, nil
}

func runtimeAppConfiguration(settings ProjectEnvironmentWorkloadSettings, specID string, artifact projectCloneArtifact, layers []DeploymentSidecarLayer) (RuntimeAppConfiguration, error) {
	settings, err := cloneWorkloadSettings(settings)
	if err != nil {
		return RuntimeAppConfiguration{}, ErrConflict
	}
	settings = normalizeRuntimeWorkloadSettings(settings)
	settingsHash, err := runtimeConfigurationHash(settings)
	if err != nil {
		return RuntimeAppConfiguration{}, err
	}
	settings.WorkPolicies, settings.QueueBindings = nil, nil
	guestHash, err := runtimeConfigurationHash(settings)
	if err != nil {
		return RuntimeAppConfiguration{}, err
	}
	artifactHash, err := runtimeConfigurationHash(normalizeProjectCloneArtifact(artifact))
	if err != nil {
		return RuntimeAppConfiguration{}, err
	}
	// Storage identities and digests are config; maintenance timestamps are not.
	type layer struct {
		Name, Key, Digest string
		Bytes             int64
	}
	rows := make([]layer, 0, len(layers))
	for _, value := range layers {
		if value.DeploymentID != artifact.ID {
			return RuntimeAppConfiguration{}, ErrConflict
		}
		rows = append(rows, layer{value.SidecarName, value.StorageKey, value.ContentDigest, value.Bytes})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	layersHash, err := runtimeConfigurationHash(rows)
	if err != nil {
		return RuntimeAppConfiguration{}, err
	}
	return RuntimeAppConfiguration{specID, settingsHash, guestHash, artifactHash, layersHash}, nil
}

// MatchesRuntimeInputs checks the separately loaded app/deployment before they
// can supply guest boot fields. Sidecar layers come from this snapshot directly.
func (snapshot RuntimeAppValuesSnapshot) MatchesRuntimeInputs(app App, dep Deployment) bool {
	if app.ID != snapshot.AppID || app.AccountID != snapshot.AccountID || dep.ID != snapshot.DeploymentID || dep.AppID != app.ID {
		return false
	}
	settings, err := WorkloadSettingsFromApp(app)
	if err != nil {
		return false
	}
	guestHash, err := runtimeConfigurationHash(normalizeRuntimeWorkloadSettings(settings))
	if err != nil || guestHash != snapshot.Configuration.GuestSettingsHash {
		return false
	}
	artifactHash, err := runtimeConfigurationHash(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(dep)))
	return err == nil && artifactHash == snapshot.Configuration.ArtifactHash
}

// SQL arrays and JSON arrays hydrate empty values differently. These App
// collections use len==0 for absence; owned work/queue pointers retain their
// distinct nil-versus-explicit-empty semantics.
func normalizeRuntimeWorkloadSettings(settings ProjectEnvironmentWorkloadSettings) ProjectEnvironmentWorkloadSettings {
	if len(settings.EgressAllowlist) == 0 {
		settings.EgressAllowlist = nil
	}
	if len(settings.EgressPorts) == 0 {
		settings.EgressPorts = nil
	}
	if len(settings.PublicAuthIPAllowlist) == 0 {
		settings.PublicAuthIPAllowlist = nil
	}
	if len(settings.DeclaredRoutes) == 0 {
		settings.DeclaredRoutes = nil
	}
	if len(settings.CORSDefaultOrigins) == 0 {
		settings.CORSDefaultOrigins = nil
	}
	if len(settings.PublicAuthBasicSealed) == 0 {
		settings.PublicAuthBasicSealed = nil
	}
	return settings
}

func runtimeConfigurationHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", ErrConflict
	}
	raw, err = canonicalRuntimeSecretJSON(raw)
	if err != nil {
		return "", ErrConflict
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
