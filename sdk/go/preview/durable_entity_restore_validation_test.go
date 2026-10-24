package faas

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPureRestoreValidatorReturnsVerdictOnly(t *testing.T) {
	request := DurableEntityRestoreValidationRequest{ProtocolVersion: 1, Event: "validate_restore", Entity: DurableEntityIdentity{AccountID: "account", AppID: "app", Namespace: "counters", Key: "counter"}, RequestID: "restore", DeploymentID: "deployment", ExpectedVersion: ^uint64(0), SourceVersion: 1, Candidate: json.RawMessage(`{"schema_version":2,"data":{"count":1}}`)}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, accept := range []bool{true, false} {
		encoded, err := EncodeDurableEntityRestoreValidation(body, func(r DurableEntityRestoreValidationRequest) error {
			if r.ExpectedVersion != ^uint64(0) {
				t.Fatal("version truncated")
			}
			if !accept {
				return errors.New("private application detail")
			}
			return nil
		})
		var verdict struct {
			ProtocolVersion int  `json:"protocol_version"`
			Valid           bool `json:"valid"`
		}
		if err != nil || json.Unmarshal(encoded, &verdict) != nil || verdict.Valid != accept || verdict.ProtocolVersion != 1 {
			t.Fatal(string(encoded), err)
		}
	}
	request.Event = "invoke"
	body, _ = json.Marshal(request)
	if _, err := EncodeDurableEntityRestoreValidation(body, func(DurableEntityRestoreValidationRequest) error {
		t.Fatal("wrong event invoked validator")
		return nil
	}); err == nil {
		t.Fatal("normal event accepted")
	}
}
