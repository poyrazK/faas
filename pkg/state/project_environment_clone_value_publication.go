package state

import "fmt"

// Fresh managed credentials need a proof of their independent resource and
// prepared envelope. A customer-value equality check cannot establish that.
var ErrProjectEnvironmentCloneManagedValueProof = fmt.Errorf("managed credential publication proof is unavailable: %w", ErrConflict)

func validateCloneValuePublication(op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource, records []projectCloneWorkloadRecord, targets map[string]projectCloneWorkloadValues) error {
	if len(records) == 0 {
		return nil
	}
	captured, err := capturedCloneValues(records)
	if err != nil || len(targets) != len(records) {
		return ErrConflict
	}
	receipts := map[string]ProjectEnvironmentCloneResource{}
	for _, resource := range resources {
		if resource.Kind != "variables" && resource.Kind != "secrets" {
			continue
		}
		key := cloneResourceKey(resource)
		if _, duplicate := receipts[key]; duplicate {
			return ErrConflict
		}
		receipts[key] = resource
	}
	if len(receipts) != len(records)*2 {
		return ErrConflict
	}
	for _, record := range records {
		for _, kind := range []string{"variables", "secrets"} {
			r := receipts[kind+"\x00"+record.WorkloadSlug]
			if r.SourceID != record.AppID || r.TargetID != record.AppID || r.SourceVersion != record.SourceValuesHash || r.Status != "ready" {
				return fmt.Errorf("clone workload %q lacks a captured %s receipt: %w", record.WorkloadSlug, kind, ErrConflict)
			}
		}
		if err := validateCloneWorkloadTargetValues(op, record, captured[record.AppID], targets[record.AppID]); err != nil {
			return err
		}
		// Standalone buckets are part of the capture even without managed
		// application secrets. A workload/value proof cannot prove their copy.
		if definitions := record.snapshot.Bindings; definitions != nil && (len(definitions.Postgres) > 0 || len(definitions.Buckets) > 0) {
			return fmt.Errorf("clone workload %q: %w", record.WorkloadSlug, ErrProjectEnvironmentCloneResourcePublicationProof)
		}
	}
	return nil
}

func validateCloneWorkloadTargetValues(op ProjectEnvironmentCloneOperation, record projectCloneWorkloadRecord, captured, target projectCloneWorkloadValues) error {
	for _, secret := range captured.Secrets {
		if cloneSecretManagedID(secret) != "" {
			return fmt.Errorf("clone workload %q: %w", record.WorkloadSlug, ErrProjectEnvironmentCloneManagedValueProof)
		}
	}
	target, err := normalizeCloneWorkloadValues(record.AppID, op.TargetEnvironment, target)
	if err != nil {
		return fmt.Errorf("clone workload %q target values are invalid: %w", record.WorkloadSlug, ErrConflict)
	}
	// Scope is an address, rather than part of the copied configuration. Map
	// only the address back to the captured source before comparing content.
	for i := range target.Variables {
		target.Variables[i].Scope = record.SourceScope
	}
	for i := range target.Secrets {
		target.Secrets[i].Scope = record.SourceScope
	}
	scopes := map[string]string{record.AppID: record.SourceScope}
	capturedHash, err := projectCloneValuesHash(scopes, captured.Variables, captured.Secrets)
	if err != nil || capturedHash != record.SourceValuesHash {
		return ErrConflict
	}
	targetHash, err := projectCloneValuesHash(scopes, target.Variables, target.Secrets)
	if err != nil || targetHash != capturedHash {
		// Configuration values and encrypted envelopes must stay out of errors.
		return fmt.Errorf("clone workload %q target values differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	}
	return nil
}
