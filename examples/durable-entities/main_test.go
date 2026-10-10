package main

// adr: 712

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestCounterComputesTransitionAndRejectsOverflow(t *testing.T) {
	for _, tc := range []struct {
		name         string
		count, delta int64
		fail         bool
	}{
		{"initial", 0, 1, false}, {"decrement", 10, -2, false},
		{"positive-overflow", math.MaxInt64, 1, true}, {"negative-overflow", math.MinInt64, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: tc.count})
			if err != nil {
				t.Fatal(err)
			}
			result, err := counter(tc.delta)(t.Context(), durableentity.View{Data: body})
			if (err != nil) != tc.fail {
				t.Fatal(result, err)
			}
			if tc.fail {
				return
			}
			var value struct {
				Count int64 `json:"count"`
			}
			if err := json.Unmarshal(result.Data, &value); err != nil || value.Count != tc.count+tc.delta {
				t.Fatal(string(result.Data), err)
			}
		})
	}
}

func TestHarnessRequiresRequestIdentityAndExplicitBucket(t *testing.T) {
	for _, args := range [][]string{nil, {"-request", "one"}} {
		var output bytes.Buffer
		if err := run(context.Background(), &output, args, func(string) string { return "" }); err == nil {
			t.Fatal("missing configuration accepted")
		}
	}
}

func TestHarnessStorageOperationsRequireOneExplicitMode(t *testing.T) {
	for _, tc := range []struct {
		request, cap, cursor, mode                           string
		cleanup, inventory, alarmStatus, outboxStatus, valid bool
		outboxRetry                                          string
	}{
		{mode: "inventory", inventory: true, valid: true},
		{mode: "alarm-status", alarmStatus: true, valid: true},
		{mode: "outbox-status", outboxStatus: true, valid: true},
		{mode: "outbox-retry", outboxRetry: "5a4f4c21-d228-4ca6-b53c-2910f5175c80", valid: true},
		{outboxStatus: true, request: "one"}, {outboxStatus: true, outboxRetry: "invalid"}, {outboxRetry: "invalid"},
		{outboxStatus: true, cursor: "invalid"}, {outboxStatus: true, inventory: true},
		{alarmStatus: true, inventory: true}, {alarmStatus: true, request: "one"},
		{alarmStatus: true, cap: "0"}, {alarmStatus: true, cursor: "invalid"},
		{cap: "0", mode: "limit", valid: true},
		{cap: "500", mode: "limit", valid: true},
		{cap: "-1"}, {cap: "01"}, {cap: "+1"}, {cap: "9223372036854775808"},
		{request: "one", inventory: true}, {cleanup: true, inventory: true},
		{inventory: true, cursor: "invalid"}, {cap: "100", request: "one"},
	} {
		mode, _, err := harnessMode(tc.request, tc.cleanup, tc.inventory, tc.alarmStatus, tc.outboxStatus, tc.outboxRetry, tc.cap, tc.cursor, nil)
		if (err == nil) != tc.valid || tc.valid && mode != tc.mode {
			t.Fatal(tc, mode, err)
		}
	}
}
