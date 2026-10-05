//go:build linux

// adr: 430 — guest dispatch rejects malformed or customer-forged probe metadata.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestAppTaskOutboundProbeRejectsUnpinnedAndUnsafeRequests(t *testing.T) {
	id := uuid.NewString()
	spec := api.OutboundBindingProbeSpec{IntegrationID: id, GatewayURL: "https://outbound.example.com", Policy: api.OutboundBindingProbePolicy{Method: "POST", Path: "/health", ExpectedStatus: 200}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []apptaskproto.Request{
		{Command: []string{api.AppTaskOutboundBindingProbeCommand, id}},
		{Command: []string{api.AppTaskOutboundBindingProbeCommand, id, string(raw)}},
		{Command: []string{api.AppTaskOutboundBindingProbeCommand, id, "{}"}, CommandShell: true},
	} {
		var stdout strings.Builder
		result, err := executeAppTaskCommand(context.Background(), request, api.AppManifest{}, nil, nil, &stdout, &strings.Builder{})
		if err != nil || result.Status != apptaskproto.StatusFailed || result.ExitCode == nil || *result.ExitCode != 1 || stdout.Len() != 0 {
			t.Fatalf("malformed request result=%+v err=%v output=%s", result, err, stdout.String())
		}
	}
}
