// adr: 488
package sched

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

var errOperationResult = errors.New("invalid managed operation result")

func decodeOperationResult(body json.RawMessage) (json.RawMessage, []exclusivework.Effect, error) {
	if len(body) > api.MaxExclusiveResultBytes {
		return nil, nil, errOperationResult
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields["gregale_operation_result"] == nil {
		if !json.Valid(body) {
			body, _ = json.Marshal(string(body))
		}
		if len(body) > api.MaxExclusiveResultBytes {
			return nil, nil, errOperationResult
		}
		return body, nil, nil
	}
	if !uniqueOperationFields(body, "gregale_operation_result", "result", "effects") {
		return nil, nil, errOperationResult
	}
	// Bound the untrusted array before allocating typed effect structs.
	array := json.NewDecoder(bytes.NewReader(fields["effects"]))
	opening, err := array.Token()
	if err != nil || opening != json.Delim('[') {
		return nil, nil, errOperationResult
	}
	count := 0
	for array.More() {
		if count >= api.MaxExclusiveEffectsPerCommit {
			return nil, nil, errOperationResult
		}
		var rawEffect json.RawMessage
		if array.Decode(&rawEffect) != nil || !uniqueOperationFields(rawEffect, "name", "webhook_id", "type", "payload") {
			return nil, nil, errOperationResult
		}
		count++
	}
	var response api.ManagedOperationResult
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || response.Version != 1 || len(response.Result) == 0 || !json.Valid(response.Result) || response.Effects == nil || len(response.Effects) > api.MaxExclusiveEffectsPerCommit {
		return nil, nil, errOperationResult
	}
	// The host resolves destination ownership; a response only proposes effects.
	for _, effect := range response.Effects {
		if effect.WebhookID == "" || effect.Type == "" {
			return nil, nil, errOperationResult
		}
	}
	effects := make([]exclusivework.Effect, len(response.Effects))
	for i, effect := range response.Effects {
		effects[i] = exclusivework.Effect(effect)
	}
	return response.Result, effects, nil
}

// Only control objects require unique fields; result/payload are opaque JSON.
func uniqueOperationFields(body []byte, allowed ...string) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !slices.Contains(allowed, key) {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	_, err = decoder.Token()
	return err == nil
}
