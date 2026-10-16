package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrReleaseConfigurationTransferUnsupported = errors.New("configuration-aware release transfers require snapshot support; use an atomic source deployment")

type ProjectReleaseConfigurationStore interface {
	PreviewProjectReleaseConfiguration(context.Context, string, string, string, json.RawMessage) (*api.ProjectReleaseConfiguration, error)
	StageProjectReleaseConfiguration(context.Context, string, string, string, *api.ProjectSafeRelease, json.RawMessage) error
}
type projectReleaseConfigContextKey struct{}

func ContextWithProjectReleaseConfigurationAttempt(ctx context.Context, attempt string) context.Context {
	return context.WithValue(ctx, projectReleaseConfigContextKey{}, attempt)
}
func projectReleaseConfigAttempt(ctx context.Context) string {
	value, _ := ctx.Value(projectReleaseConfigContextKey{}).(string)
	return value
}

type projectReleaseVariable struct {
	AccountID string    `json:"account_id"`
	AppID     string    `json:"app_id"`
	Scope     string    `json:"scope"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func releaseVariables(rows []AppEnv) []projectReleaseVariable {
	out := make([]projectReleaseVariable, 0, len(rows))
	for _, r := range rows {
		out = append(out, projectReleaseVariable(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
func releaseVariableRows(rows []projectReleaseVariable) []AppEnv {
	out := make([]AppEnv, 0, len(rows))
	for _, r := range rows {
		out = append(out, AppEnv(r))
	}
	return out
}

type projectReleaseConfigVersion struct {
	Version int64           `json:"version"`
	Hash    string          `json:"config_hash"`
	Values  json.RawMessage `json:"values"`
}
type projectReleaseConfigWorkload struct {
	AppID            string                      `json:"app_id"`
	Name             string                      `json:"name"`
	DeploymentID     string                      `json:"deployment_id"`
	Legacy           []projectReleaseVariable    `json:"legacy"`
	Snapshot         *[]projectReleaseVariable   `json:"snapshot,omitempty"`
	Target           []projectReleaseVariable    `json:"target"`
	OtherVariables   int                         `json:"other_variables"`
	LiveSecrets      []runtimeAppSealedSecretRow `json:"live_secrets,omitempty"`
	SecretSnapshot   *[]releaseSecretVersion     `json:"secret_snapshot,omitempty"`
	SecretTarget     []releaseSecretVersion      `json:"secret_target,omitempty"`
	SecretIneligible bool                        `json:"secret_ineligible,omitempty"`
	SecretKeys       []string                    `json:"secret_keys"`
}
type projectReleaseConfigCapture struct {
	SecretVersions map[string]releaseSecretVersion  `json:"-"`
	Plan           api.Plan                         `json:"plan"`
	ProjectID      string                           `json:"project_id"`
	BaselineID     string                           `json:"baseline_id"`
	Previous       projectReleaseConfigVersion      `json:"previous"`
	Target         json.RawMessage                  `json:"target"`
	Request        json.RawMessage                  `json:"request,omitempty"`
	Descriptor     *api.ProjectReleaseConfiguration `json:"descriptor,omitempty"`
	Workloads      []projectReleaseConfigWorkload   `json:"workloads"`
}

func (capture *projectReleaseConfigCapture) prepare(accountID string, request json.RawMessage, limits api.Limits) (*api.ProjectReleaseConfiguration, error) {
	inherited := false
	for _, w := range capture.Workloads {
		inherited = inherited || w.Snapshot != nil || w.SecretSnapshot != nil
	}
	if len(request) == 0 && !inherited {
		return nil, nil
	}
	var input api.ProjectReleaseConfigRequest
	var requestHash string
	if len(request) > 0 {
		canonical, hash, err := api.NormalizeProjectReleaseConfig(request)
		if err != nil {
			return nil, err
		}
		capture.Request, requestHash = canonical, hash
		if err := json.Unmarshal(canonical, &input); err != nil {
			return nil, err
		}
	}
	previous, previousHash, err := api.NormalizeProjectEnvironmentConfig(capture.Previous.Values)
	if err != nil || capture.Previous.Hash != "" && capture.Previous.Hash != previousHash {
		return nil, ErrConflict
	}
	capture.Previous.Values, capture.Previous.Hash = previous, previousHash
	var target map[string]json.RawMessage
	if json.Unmarshal(previous, &target) != nil {
		return nil, ErrConflict
	}
	env := map[string]map[string]string{}
	if raw := target["env"]; len(raw) > 0 && json.Unmarshal(raw, &env) != nil {
		return nil, fmt.Errorf("existing environment configuration has an incompatible env field")
	}
	used := map[string]bool{}
	now := time.Now().UTC()
	for i := range capture.Workloads {
		w := &capture.Workloads[i]
		sort.Strings(w.SecretKeys)
		w.Target = append([]projectReleaseVariable{}, w.Legacy...)
		if w.Snapshot != nil {
			w.Target = append([]projectReleaseVariable{}, (*w.Snapshot)...)
		}
		if variables, ok := input.Env[w.Name]; ok {
			used[w.Name] = true
			env[w.Name] = variables
			w.Target = []projectReleaseVariable{}
			for key, value := range variables {
				if api.ValidateEnvKey(key) != nil || len(value) > limits.EnvValueMaxBytes {
					return nil, fmt.Errorf("release variables exceed the environment key or value limits")
				}
				for _, secret := range w.SecretKeys {
					if secret == key {
						return nil, fmt.Errorf("release variables cannot shadow a secret or managed binding")
					}
				}
				w.Target = append(w.Target, projectReleaseVariable{accountID, w.AppID, "production", key, value, now, now})
			}
		}
		if len(w.Target)+w.OtherVariables > limits.EnvVarsMax {
			return nil, fmt.Errorf("release variables exceed the app environment-key limit")
		}
		sort.Slice(w.Target, func(i, j int) bool { return w.Target[i].Key < w.Target[j].Key })
	}
	if len(used) != len(input.Env) {
		return nil, fmt.Errorf("release config references an unknown project workload")
	}
	if len(request) > 0 {
		target["env"], _ = json.Marshal(env)
	}
	targetRaw, _ := json.Marshal(target)
	canonical, targetHash, err := api.NormalizeProjectEnvironmentConfig(targetRaw)
	if err != nil {
		return nil, err
	}
	capture.Target = canonical
	secretPins := len(input.Secrets) > 0
	for _, w := range capture.Workloads {
		secretPins = secretPins || w.SecretSnapshot != nil
	}
	proof := []any{}
	for _, w := range capture.Workloads {
		proof = append(proof, struct {
			AppID, Name    string
			Variables      []projectReleaseVariable
			OtherVariables int
			SecretKeys     []string
		}{w.AppID, w.Name, w.Legacy, w.OtherVariables, w.SecretKeys})
		if secretPins {
			proof = append(proof, struct {
				AppID, SecretsHash string
				Ineligible         bool
			}{w.AppID, releaseSecretRowsHash(w.LiveSecrets), w.SecretIneligible})
		}
	}
	encoded, _ := json.Marshal(proof)
	capture.Descriptor = &api.ProjectReleaseConfiguration{PreviousVersion: capture.Previous.Version, PreviousHash: previousHash, TargetHash: targetHash, EnvironmentHash: fmt.Sprintf("%x", sha256.Sum256(encoded)), RequestHash: requestHash}
	if err := capture.prepareSecrets(input, limits); err != nil {
		return nil, err
	}
	return capture.Descriptor, nil
}
func matchingReleaseConfiguration(a, b *api.ProjectReleaseConfiguration) bool {
	return api.ProjectReleaseConfigurationPolicyID(a) == api.ProjectReleaseConfigurationPolicyID(b)
}
func candidateReleaseVariables(capture projectReleaseConfigCapture, d Deployment) ([]projectReleaseVariable, error) {
	if d.Scope != "production" || !d.TrafficPercentExplicit || d.TrafficPercent != 0 || d.submissionAttemptID != ProjectWorkloadAttempt(d.releaseConfigAttempt, d.AppID) {
		return nil, ErrConflict
	}
	for _, w := range capture.Workloads {
		if w.AppID == d.AppID {
			return append([]projectReleaseVariable{}, w.Target...), nil
		}
	}
	return nil, ErrConflict
}
