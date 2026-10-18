package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestDomainCommandsRequireExplicitAuditAndRetirementEvidence(t *testing.T) {
	id, keyID := uuid.NewString(), uuid.NewString()
	audit := []string{"--request-id", uuid.NewString(), "--actor", "operator.test", "--reason", "rotation_reviewed", "--evidence", "ticket:123"}
	for _, name := range []string{"stage-key", "verify-key", "activate-key", "retire-key", "reverify"} {
		args := []string{"domains", name, "--id", id}
		if name != "stage-key" && name != "reverify" {
			args = append(args, "--key-id", keyID)
		}
		if name == "retire-key" {
			args = append(args, "--confirmed-queue-drained")
		}
		args = append(args, audit...)
		cmd, err := parseCommand(args, &bytes.Buffer{})
		if err != nil || cmd.method != http.MethodPost || !strings.HasPrefix(cmd.route, internalmail.OperationsPath+"/domains/"+id) {
			t.Fatal(name, err)
		}
		raw, err := json.Marshal(cmd.input)
		if err != nil || !bytes.Contains(raw, []byte(`"actor":"operator.test"`)) {
			t.Fatal("missing flat audit fields", err)
		}
	}
	for _, args := range [][]string{
		{"domains", "stage-key", "--id", id},
		append([]string{"domains", "activate-key", "--id", id}, audit...),
		append([]string{"domains", "retire-key", "--id", id, "--key-id", keyID}, audit...),
		{"domains", "keys", "--id", id, "--limit", "101"},
	} {
		if _, err := parseCommand(args, &bytes.Buffer{}); err == nil {
			t.Fatal("unsafe domain command accepted", args)
		}
	}
}

func TestDomainCLIGatesBeforeOperatorSecretAndReportsNegativeObservation(t *testing.T) {
	id := uuid.NewString()
	for _, args := range [][]string{{"domains", "keys", "--id", id}, {"domains", "reverify", "--id", id, "--request-id", uuid.NewString(), "--actor", "operator.test", "--reason", "reviewed", "--evidence", "ticket:123"}} {
		var out, diag bytes.Buffer
		code := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.RotationEnableEnv, internalmail.ReverifyEnableEnv:
				return "0"
			}
			t.Fatal("disabled lifecycle read private configuration", key)
			return ""
		}, args, &out, &diag)
		if code != 1 || !strings.Contains(diag.String(), "disabled") {
			t.Fatal(code, diag.String())
		}
	}
	for _, outcome := range []string{"mismatch", "unavailable", "valid"} {
		raw := json.RawMessage(`{"result":{"check_result":"` + outcome + `"}}`)
		if domainObservationFailed(internalmail.OperationsPath+"/domains/"+id+"/reverify", raw) != (outcome != "valid") {
			t.Fatal("negative DNS observation reported success")
		}
	}
	if !domainObservationFailed(internalmail.OperationsPath+"/domains/"+id+"/keys/"+uuid.NewString()+"/activate", json.RawMessage(`{"result":{"state":"staged","check_result":"unavailable"}}`)) {
		t.Fatal("failed activation reported success")
	}
}
