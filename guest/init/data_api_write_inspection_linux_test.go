//go:build linux

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestDataAPIWriteInspectionRejectsUnsafeRequestWithoutLeakingSecrets(t *testing.T) {
	nonce := strings.Repeat("a", 64)
	for _, req := range []apptaskproto.Request{
		{Version: apptaskproto.DataAPIInspectionVersion, Command: []string{api.AppTaskDataAPIWriteInspectionCommand, "notes;drop", nonce}},
		{Version: apptaskproto.DataAPIInspectionVersion, Command: []string{api.AppTaskDataAPIWriteInspectionCommand, "notes", nonce}, Interactive: true},
		{Version: apptaskproto.DataAPIInspectionVersion, Command: []string{api.AppTaskDataAPIWriteInspectionCommand, "notes", nonce}},
		{Version: apptaskproto.Version, Command: []string{api.AppTaskDataAPIWriteInspectionCommand, "notes", nonce}},
	} {
		var stdout strings.Builder
		result, err := executeDataAPIWriteInspection(context.Background(), req, api.AppManifest{}, map[string]string{"DATABASE_URL": "postgres://secret-user:secret-password@secret-host/db?sslmode=disable"}, nil, &stdout)
		if err != nil || result.Status != apptaskproto.StatusFailed || result.ExitCode == nil || *result.ExitCode != 1 || result.FailureCode != "data_api_inspection_unavailable" || stdout.Len() != 0 || strings.Contains(result.FailureMessage, "secret-") {
			t.Fatalf("unsafe request did not fail closed: %+v output=%q error=%v", result, stdout.String(), err)
		}
	}
}
