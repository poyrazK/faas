package state

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Generated values are accepted only when they agree with the source's frozen
// declarations. The destination owns binding assignments and transport.
func (c *promotionReleaseConfiguration) prepareServiceBindings(w *promotionReleaseWorkload, maxVariables int) error {
	source := api.ServiceBindingEnvForTransport(nil, w.SourceSettings.Manifest.ServiceBindings, w.SourceSettings.Manifest.ServiceBindingTransport)
	target := api.ServiceBindingEnvForTransport(nil, w.TargetSettings.Manifest.ServiceBindings, w.TargetSettings.Manifest.ServiceBindingTransport)
	generated := len(source) > 0 || len(target) > 0
	for _, binding := range w.TargetSettings.Manifest.ServiceBindings {
		if binding.Binding != api.ServiceBindingEnvKey(binding.Service) || !c.hasService(binding.Service, w.AppID) {
			return ErrReleaseConfigurationTransferUnsupported
		}
	}
	for key := range source {
		if _, exists := target[key]; !exists {
			return ErrReleaseConfigurationTransferUnsupported
		}
	}
	for _, key := range w.ReservedKeys {
		if _, collision := target[key]; collision {
			return ErrInvalidArgument
		}
	}
	values := map[string]projectReleaseVariable{}
	for _, row := range w.SourceVariables {
		if strings.HasPrefix(row.Key, api.ServiceBindingEnvPrefix) {
			if expected, ok := source[row.Key]; !ok || expected != row.Value {
				return ErrReleaseConfigurationTransferUnsupported
			}
			generated = true
			continue
		}
		values[row.Key] = row
	}
	var overrides map[string]string
	if nonemptySecretJSON(w.Source.OverrideEnv) && json.Unmarshal(w.Source.OverrideEnv, &overrides) != nil {
		return ErrReleaseConfigurationTransferUnsupported
	}
	for key, value := range overrides {
		if strings.HasPrefix(key, api.ServiceBindingEnvPrefix) {
			if expected, ok := source[key]; !ok || expected != value {
				return ErrReleaseConfigurationTransferUnsupported
			}
		}
	}
	for key, value := range w.SourceSettings.Manifest.Env {
		if strings.HasPrefix(key, api.ServiceBindingEnvPrefix) {
			if expected, ok := source[key]; !ok || expected != value {
				return ErrReleaseConfigurationTransferUnsupported
			}
		}
	}
	if !generated {
		return nil
	}
	for key, value := range target {
		values[key] = projectReleaseVariable{AccountID: c.AccountID, AppID: w.AppID, Scope: c.From, Key: key, Value: value}
	}
	if len(values)+w.OtherVariables > maxVariables {
		return ErrInvalidArgument
	}
	rows := make([]projectReleaseVariable, 0, len(values))
	for _, row := range values {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	w.PromotedVariables = &rows
	// Only override keys originally owned by the platform are rewritten. Runtime
	// pins supply any remaining generated values, including added bindings.
	for key := range overrides {
		if strings.HasPrefix(key, api.ServiceBindingEnvPrefix) {
			delete(overrides, key)
			if value, ok := target[key]; ok {
				overrides[key] = value
			}
		}
	}
	if nonemptySecretJSON(w.Source.OverrideEnv) {
		raw, err := json.Marshal(overrides)
		if err != nil {
			return err
		}
		message := json.RawMessage(raw)
		w.PromotedOverrideEnv = &message
	}
	return nil
}

func (c promotionReleaseConfiguration) hasService(name, caller string) bool {
	count := 0
	for _, peer := range c.Workloads {
		if peer.AppID != caller && peer.SourceSettings.WorkloadClass != WorkloadClassWorker && peer.TargetSettings.WorkloadClass != WorkloadClassWorker && (peer.Name == name || peer.Slug == name) {
			count++
		}
	}
	return count == 1
}

func (c *promotionReleaseConfiguration) prepareDestinationConfig() error {
	needed := false
	for _, w := range c.Workloads {
		needed = needed || w.PromotedVariables != nil
	}
	if !needed {
		return nil
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(c.SourceConfig.Values, &document) != nil {
		return ErrConflict
	}
	env := map[string]map[string]string{}
	if raw, ok := document["env"]; ok && json.Unmarshal(raw, &env) != nil {
		return ErrConflict
	}
	for _, w := range c.Workloads {
		values := map[string]string{}
		for _, row := range promotedReleaseVariables(w, c.To) {
			values[row.Key] = row.Value
		}
		env[w.Name] = values
	}
	document["env"], _ = json.Marshal(env)
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	canonical, hash, err := api.NormalizeProjectEnvironmentConfig(raw)
	if err != nil {
		return err
	}
	c.DestinationConfig = &projectReleaseConfigVersion{Hash: hash, Values: canonical}
	return nil
}

func promotedReleaseArtifact(w promotionReleaseWorkload) projectCloneArtifact {
	a := w.Source
	if w.PromotedOverrideEnv != nil {
		a.OverrideEnv = *w.PromotedOverrideEnv
	}
	return normalizePromotionArtifact(a)
}

func promotionDestinationConfig(p ProjectEnvironmentPromotion, c *promotionReleaseConfiguration) (json.RawMessage, string) {
	if c != nil && c.DestinationConfig != nil {
		return c.DestinationConfig.Values, c.DestinationConfig.Hash
	}
	return p.SourceConfigSnapshot, p.SourceConfigHash
}
