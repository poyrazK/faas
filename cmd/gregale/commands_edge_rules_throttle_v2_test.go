package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-909: --throttle-key-field and --throttle-count-status reach the action.
func TestBuildEdgeRuleAction_ThrottleCompositeAndCountStatuses(t *testing.T) {
	raw, err := buildEdgeRuleAction("throttle", edgeRuleActionInputs{
		ThrottleRPS: 0.1, ThrottleBurst: 5, ThrottleKeyBy: api.ThrottleKeyByComposite,
		ThrottleKeyFields:     []string{"ip", "header:x-username"},
		ThrottleCountStatuses: []string{"401", "403"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var a api.EdgeRuleThrottleAction
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.KeyFields, []string{"ip", "header:x-username"}) || !reflect.DeepEqual(a.CountStatuses, []int{401, 403}) {
		t.Fatalf("action = %+v", a)
	}
	_, err = buildEdgeRuleAction("throttle", edgeRuleActionInputs{ThrottleRPS: 1, ThrottleBurst: 1, ThrottleCountStatuses: []string{"four-oh-one"}})
	if err == nil || !strings.Contains(err.Error(), "not an HTTP status") {
		t.Fatalf("bad status err = %v", err)
	}
}
