package main

import (
	"context"
	"testing"
)

func TestRuntimeDevPatchAckRecordsFirstAcknowledgement(t *testing.T) {
	f := newRuntimeDevPatchFixture(t, true, true)
	f.publish(t, "patch", false)
	response := sendRuntimeConfigTestRequest(t, f.receiver, runtimeConfigRequest{Kind: runtimeDevPatchAckKind, PatchGeneration: 1, PatchApplyMS: 120})
	if !response.Accepted || response.Error != "" {
		t.Fatalf("ack response = %+v", response)
	}
	status, err := f.store.DevSourcePatchStatus(context.Background(), f.app.ID, 1)
	if err != nil || status.AppliedAt == nil || status.ApplyMS != 120 {
		t.Fatalf("status after ack = %+v, %v", status, err)
	}
}

func TestRuntimeDevPatchAckValidation(t *testing.T) {
	cases := []runtimeConfigRequest{
		{Kind: runtimeDevPatchAckKind},
		{Kind: runtimeDevPatchAckKind, PatchGeneration: 1, PatchApplyMS: -1},
		{Kind: runtimeDevPatchAckKind, PatchGeneration: 1, PatchApplyMS: 600001},
		{Kind: runtimeDevPatchAckKind, PatchGeneration: 1, ErrorCode: "Not A Code"},
		{Kind: runtimeDevPatchAckKind, PatchGeneration: 1, Revision: "x"},
		{Kind: runtimeDevPatchKind, PatchApplyMS: 5},
		{Kind: "env", PatchApplyMS: 5},
	}
	for _, request := range cases {
		f := newRuntimeDevPatchFixture(t, true, true)
		if response := sendRuntimeConfigTestRequest(t, f.receiver, request); response.Error != "invalid_request" {
			t.Fatalf("request %+v = %+v, want invalid_request", request, response)
		}
	}
}
