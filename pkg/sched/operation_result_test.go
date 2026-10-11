// adr: 488
package sched

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestDecodeOperationResult(t *testing.T) {
	for _, tc := range []struct {
		name, body, result string
		fail               bool
		effects            int
	}{
		{"ordinary", `{"ok":true}`, `{"ok":true}`, false, 0},
		{"ordinary-array", `[1,2]`, `[1,2]`, false, 0},
		{"ordinary-text", `done`, `"done"`, false, 0},
		{"envelope", `{"gregale_operation_result":1,"result":{"ok":true},"effects":[{"name":"notify","webhook_id":"d1a1bfe7-6eb7-4546-99c7-af291b68cf5f","type":"order.fulfilled","payload":{"order_id":1}}]}`, `{"ok":true}`, false, 1},
		{"null-result", `{"gregale_operation_result":1,"result":null,"effects":[]}`, `null`, false, 0},
		{"duplicate-version", `{"gregale_operation_result":2,"gregale_operation_result":1,"result":{},"effects":[]}`, "", true, 0},
		{"duplicate-destination", `{"gregale_operation_result":1,"result":{},"effects":[{"name":"notify","webhook_id":"first","webhook_id":"second","type":"order.created","payload":{}}]}`, "", true, 0},
		{"future-version", `{"gregale_operation_result":2,"result":{},"effects":[]}`, "", true, 0},
		{"missing-result", `{"gregale_operation_result":1,"effects":[]}`, "", true, 0},
		{"missing-effects", `{"gregale_operation_result":1,"result":{}}`, "", true, 0},
		{"null-effects", `{"gregale_operation_result":1,"result":{},"effects":null}`, "", true, 0},
		{"incorrect-control-case", `{"gregale_operation_result":1,"Result":{},"effects":[]}`, "", true, 0},
		{"unknown-field", `{"gregale_operation_result":1,"result":{},"effects":[],"tenant_id":"forged"}`, "", true, 0},
		{"unknown-effect-field", `{"gregale_operation_result":1,"result":{},"effects":[{"name":"notify","webhook_id":"id","type":"order.fulfilled","payload":{},"account_id":"forged"}]}`, "", true, 0},
		{"opaque-effect", `{"gregale_operation_result":1,"result":{},"effects":[{"name":"notify","payload":{}}]}`, "", true, 0},
		{"oversized", strings.Repeat("x", api.MaxExclusiveResultBytes+1), "", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, effects, err := decodeOperationResult(json.RawMessage(tc.body))
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v", err)
			}
			if !tc.fail && (string(result) != tc.result || len(effects) != tc.effects) {
				t.Fatalf("result=%s effects=%+v", result, effects)
			}
		})
	}
}

func TestDecodeOperationResultBoundsEffectCount(t *testing.T) {
	for _, count := range []int{api.MaxExclusiveEffectsPerCommit, api.MaxExclusiveEffectsPerCommit + 1} {
		effects := make([]api.ManagedOperationEffect, count)
		for i := range effects {
			effects[i] = api.ManagedOperationEffect{Name: "notify", WebhookID: "d1a1bfe7-6eb7-4546-99c7-af291b68cf5f", Type: "order.fulfilled", Payload: json.RawMessage(`null`)}
		}
		body, err := json.Marshal(api.ManagedOperationResult{Version: 1, Result: json.RawMessage(`null`), Effects: effects})
		if err != nil {
			t.Fatal(err)
		}
		_, decoded, err := decodeOperationResult(body)
		if count <= api.MaxExclusiveEffectsPerCommit {
			if err != nil || len(decoded) != count {
				t.Fatalf("bounded count=%d decoded=%d err=%v", count, len(decoded), err)
			}
		} else if err == nil {
			t.Fatal("oversized effect array accepted")
		}
	}
}
