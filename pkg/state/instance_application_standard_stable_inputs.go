package state

// adr: 430. Immutable producer identity and renewable approval have separate lives.

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
)

// Historical captures keep their original bytes and hashes. Comparing their
// stable projection never replaces the independently fresh approval check.
func standardNativeRuntimeInputsMatch(captured, current []byte) (bool, error) {
	return compareStandardRuntimeInputs(captured, current, false)
}

func compareStandardRuntimeInputs(captured, current []byte, allowUnmanagedPlan bool) (bool, error) {
	inputs := make([]map[string]any, 2)
	for i, raw := range [][]byte{captured, current} {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&inputs[i]); err != nil {
			return false, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return false, ErrApplicationStandardRuntimeStale
		}
		if inputs[i] == nil {
			return false, ErrApplicationStandardRuntimeStale
		}
		if err := projectStandardStableArtifact(inputs[i]); err != nil {
			return false, err
		}
	}
	if allowUnmanagedPlan && unmanagedStandardRuntimeInputs(inputs[0]) && unmanagedStandardRuntimeInputs(inputs[1]) {
		delete(inputs[0], "account_plan")
		delete(inputs[1], "account_plan")
	}
	return reflect.DeepEqual(inputs[0], inputs[1]), nil
}

func projectStandardStableArtifact(input map[string]any) error {
	identity, exists := input["runtime_artifacts"]
	if !exists {
		return nil // A compatibility mirror cannot create producer lineage.
	}
	artifact, ok := input["artifact"].(map[string]any)
	if !ok {
		return ErrApplicationStandardRuntimeStale
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	text := func(value any) string { s, _ := value.(string); return s }
	if _, _, err := decodeRuntimeArtifactCapture(raw, text(input["account_id"]), text(input["org_id"]), text(input["app_id"]), text(artifact["id"]), text(artifact["scope"])); err != nil {
		return err
	}
	removeStandardApprovalMirrors(artifact)
	return nil
}

func removeStandardApprovalMirrors(artifact map[string]any) {
	delete(artifact, "scan_status")
	delete(artifact, "scan_result_hash")
}
