package state

import (
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Secret changes always belong to the destination. Source credentials never
// enter this capture, including when the source already has immutable pins.
func (c *promotionReleaseConfiguration) prepareSecrets(p ProjectEnvironmentPromotion, runtime promotionRuntimeReader) error {
	if len(p.ReleaseSecrets) == 0 {
		return nil
	}
	if p.ToEnvironment != "production" || p.Migrate || p.Workers {
		return ErrReleaseConfigurationTransferUnsupported
	}
	raw, _ := json.Marshal(api.ProjectReleaseConfigRequest{Secrets: p.ReleaseSecrets})
	canonical, _, err := api.NormalizeProjectReleaseConfig(raw)
	if err != nil {
		return ErrInvalidArgument
	}
	var request api.ProjectReleaseConfigRequest
	if json.Unmarshal(canonical, &request) != nil {
		return ErrInvalidArgument
	}
	input := projectReleaseConfigCapture{ProjectID: c.ProjectID, SecretVersions: c.SecretVersions, Descriptor: &api.ProjectReleaseConfiguration{}}
	for _, w := range c.Workloads {
		if !promotionSnapshotHTTP(w.SourceSettings, w.Source) || !promotionSnapshotHTTP(w.TargetSettings, w.Previous) ||
			w.SecretIneligible {
			return ErrReleaseConfigurationTransferUnsupported
		}
		snapshot, err := runtime(c.AccountID, w.AppID, w.Previous.ID)
		if err != nil {
			return err
		}
		baseline, err := promotionSecretBaseline(snapshot)
		if err != nil {
			return err
		}
		rows := append([]runtimeAppSealedSecretRow{}, w.TargetLiveSecrets...)
		input.Workloads = append(input.Workloads, projectReleaseConfigWorkload{AppID: w.AppID, Name: w.Name, LiveSecrets: rows, SecretSnapshot: &baseline, Target: w.SourceVariables})
	}
	if err := input.prepareSecrets(request, api.MustLimitsFor(c.Plan)); err != nil {
		return ErrInvalidArgument
	}
	c.RequestedSecrets = request.Secrets
	c.Secrets = input.Descriptor.Secrets
	for i, w := range input.Workloads {
		c.Workloads[i].SecretSnapshot = w.SecretSnapshot
		c.Workloads[i].SecretTarget = w.SecretTarget
	}
	return nil
}

func promotionSecretBaseline(snapshot RuntimeAppValuesSnapshot) ([]releaseSecretVersion, error) {
	rows := []releaseSecretVersion{}
	for _, secret := range snapshot.Secrets {
		row := releaseSecretRow(secret)
		if defaultSecretClass(row.SecretClass) != api.SecretClassPersistent || row.ManagedCredentialRef != "" || row.ManagedPostgresBindingID != "" || row.ManagedObjectStorageCredentialID != "" {
			return nil, ErrReleaseConfigurationTransferUnsupported
		}
		// Envelope rotation must not invalidate a preview of a legacy baseline.
		logical := row
		logical.Kid, logical.Ciphertext = "", nil
		version := releaseSecretFromRow(snapshot.AppID, logical)
		version.runtimeAppSealedSecretRow = row
		if snapshot.ReleaseSecretsPinned {
			version.ID = snapshot.ReleaseSecretVersionIDs[row.Key]
		}
		if !api.ValidReleaseSecretVersionID(version.ID) {
			return nil, ErrConflict
		}
		rows = append(rows, version)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

func promotionPinnedSecretsHash(snapshot RuntimeAppValuesSnapshot, rows []releaseSecretVersion) (string, error) {
	if err := applyReleaseSecretSnapshot(&snapshot, rows); err != nil {
		return "", err
	}
	return promotionSecretsHash(snapshot)
}

func (c promotionReleaseConfiguration) secretVersions() map[string]releaseSecretVersion {
	versions := map[string]releaseSecretVersion{}
	for _, w := range c.Workloads {
		for _, row := range w.SecretTarget {
			versions[row.ID] = row
		}
	}
	return versions
}

func promotionSecretRequest(refs map[string]map[string]string) json.RawMessage {
	raw, _ := json.Marshal(api.ProjectReleaseConfigRequest{Secrets: refs})
	return raw
}

func clonePromotionSecrets(config *api.ProjectReleaseSecretConfiguration) *api.ProjectReleaseSecretConfiguration {
	if config == nil {
		return nil
	}
	raw, _ := json.Marshal(config)
	var out api.ProjectReleaseSecretConfiguration
	_ = json.Unmarshal(raw, &out)
	return &out
}
