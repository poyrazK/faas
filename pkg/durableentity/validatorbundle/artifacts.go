// adr: 947
package validatorbundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/durableentity/providerconfig"
)

var ErrArtifactUnavailable = errors.New("validator artifact unavailable or invalid")
var ErrBindingConflict = errors.New("validator deployment binding conflict")

// Artifacts contains only private platform storage. There is deliberately no
// deletion or rebinding operation: release and rollback references stay intact.
type Artifacts struct {
	store durableentity.ObjectStore
	apps  map[string]bool
}
type binding struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
	SHA256       string `json:"sha256"`
}
type content struct {
	Runtime    api.ExecutionRuntime `json:"runtime"`
	Entrypoint string               `json:"entrypoint"`
	Files      []api.ExecutionFile  `json:"files"`
}

func NewArtifacts(store durableentity.ObjectStore, apps map[string]bool) *Artifacts {
	copied := map[string]bool{}
	for id, allowed := range apps {
		copied[id] = allowed
	}
	return &Artifacts{store: store, apps: copied}
}

// OpenArtifacts configures a shared bucket independently of application state.
// Provider settings use the FAAS_DURABLE_ENTITY_VALIDATOR_* prefix; normal ADC
// or AWS credentials remain the provider's established secret mechanism.
func OpenArtifacts(getenv func(string) string) (*Artifacts, error) {
	if getenv("FAAS_DURABLE_ENTITY_VALIDATOR_ARTIFACTS_ENABLED") != "1" {
		return nil, nil
	}
	if getenv("FAAS_DURABLE_ENTITY_VALIDATOR_PROVIDER") == "" {
		return nil, ErrArtifactUnavailable
	}
	selectEnv := func(key string) string {
		switch key {
		case "GREGALE_ENTITY_PROVIDER":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_PROVIDER")
		case "GREGALE_ENTITY_BUCKET":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_BUCKET")
		case "GREGALE_ENTITY_ENDPOINT":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_ENDPOINT")
		case "GREGALE_ENTITY_REGION":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_REGION")
		case "GREGALE_ENTITY_GCS_IMPERSONATE_SERVICE_ACCOUNT":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_GCS_IMPERSONATE_SERVICE_ACCOUNT")
		case "AWS_ACCESS_KEY_ID":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_ACCESS_KEY")
		case "AWS_SECRET_ACCESS_KEY":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_SECRET_KEY")
		case "AWS_SESSION_TOKEN":
			return getenv("FAAS_DURABLE_ENTITY_VALIDATOR_SESSION_TOKEN")
		default:
			return getenv(key)
		}
	}

	selection, err := providerconfig.Open(selectEnv)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	store, err := durableentity.NewProviderStore(selection.Provider, selection.Bucket)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	apps := map[string]bool{}
	for _, part := range strings.Split(getenv("FAAS_DURABLE_ENTITY_APPS"), ",") {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil || id == uuid.Nil {
			return nil, ErrArtifactUnavailable
		}
		apps[id.String()] = true
	}
	return NewArtifacts(store, apps), nil
}

func identity(appID, deploymentID string) bool {
	a, e1 := uuid.Parse(appID)
	d, e2 := uuid.Parse(deploymentID)
	return e1 == nil && e2 == nil && a != uuid.Nil && d != uuid.Nil && a.String() == appID && d.String() == deploymentID
}
func artifactKey(appID, hash string) string {
	return "gregale/durable-entity-validators/v1/apps/" + appID + "/bundles/" + hash + ".json"
}
func bindingKey(appID, deploymentID string) string {
	return "gregale/durable-entity-validators/v1/apps/" + appID + "/deployments/" + deploymentID + ".json"
}
func strict(body []byte, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value) == nil && decoder.Decode(new(any)) == io.EOF
}

func (a *Artifacts) Publish(ctx context.Context, b Bundle) error {
	if a == nil || a.store == nil || !a.apps[b.AppID] || Validate(b) != nil {
		return ErrArtifactUnavailable
	}
	body, err := json.Marshal(content{b.Runtime, b.Entrypoint, b.Files})
	if err != nil || len(body) > api.MaxDurableEntityValidatorRegistryBytes {
		return ErrArtifactUnavailable
	}
	if err = a.putImmutable(ctx, artifactKey(b.AppID, b.SHA256), body); err != nil {
		return err
	}
	// Never publish a binding before independently verifying referenced bytes.
	if _, err = a.loadContent(ctx, b.AppID, b.DeploymentID, b.SHA256); err != nil {
		return err
	}
	metadata, _ := json.Marshal(binding{b.AppID, b.DeploymentID, b.SHA256})
	return a.putImmutable(ctx, bindingKey(b.AppID, b.DeploymentID), metadata)
}

func (a *Artifacts) putImmutable(ctx context.Context, key string, body []byte) error {
	version, err := a.store.Put(ctx, key, body, "")
	if err == nil && version != "" {
		return nil
	}
	// A lost acknowledgement may already have committed. Confirm the identical
	// bytes before accepting success; never overwrite or retry an unknown write.
	actual, _, readErr := a.store.Get(ctx, key, int64(api.MaxDurableEntityValidatorRegistryBytes))
	if readErr == nil && bytes.Equal(actual, body) {
		return nil
	}
	if readErr == nil {
		return ErrBindingConflict
	}
	return ErrArtifactUnavailable
}

func (a *Artifacts) Resolve(ctx context.Context, appID, deploymentID string) (Bundle, error) {
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	if a == nil || a.store == nil || !a.apps[appID] || !identity(appID, deploymentID) {
		return Bundle{}, ErrArtifactUnavailable
	}
	body, _, err := a.store.Get(ctx, bindingKey(appID, deploymentID), int64(api.MaxDurableEntityRestoreValidationBytes))
	var ref binding
	if err != nil || !strict(body, &ref) || ref.AppID != appID || ref.DeploymentID != deploymentID {
		return Bundle{}, ErrArtifactUnavailable
	}
	return a.loadContent(ctx, appID, deploymentID, ref.SHA256)
}

func (a *Artifacts) loadContent(ctx context.Context, appID, deploymentID, hash string) (Bundle, error) {
	if len(hash) != sha256.Size*2 || strings.Trim(hash, "0123456789abcdef") != "" {
		return Bundle{}, ErrArtifactUnavailable
	}
	body, _, err := a.store.Get(ctx, artifactKey(appID, hash), int64(api.MaxDurableEntityValidatorRegistryBytes))
	var c content
	if err != nil || !strict(body, &c) {
		return Bundle{}, ErrArtifactUnavailable
	}
	b := Bundle{AppID: appID, DeploymentID: deploymentID, SHA256: hash, Runtime: c.Runtime, Entrypoint: c.Entrypoint, Files: c.Files}
	if Validate(b) != nil {
		return Bundle{}, ErrArtifactUnavailable
	}
	return b, nil
}

// Check is a lifecycle preflight; applications outside the allowlist are unaffected.
func (a *Artifacts) Check(ctx context.Context, appID, deploymentID string) error {
	if a == nil || !a.apps[appID] {
		return nil
	}
	_, err := a.Resolve(ctx, appID, deploymentID)
	return err
}
