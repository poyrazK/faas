package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestCIExecutionRerunRequestPreservesParentAndExplicitIdentity(t *testing.T) {
	parent := uuid.NewString()
	options := ciExecutionOptions{id: parent, key: "manual-rerun"}
	method, path, body, headers, err := ciExecutionRequest("rerun", options)
	raw, marshalErr := json.Marshal(body)
	if err != nil || marshalErr != nil || method != http.MethodPost || path != "/v1/internal/ci/executions/"+parent+"/rerun" || headers.Get("Idempotency-Key") != options.key || string(raw) != "{}" {
		t.Fatal("rerun lost parent/request identity or copied configuration")
	}
	for _, change := range []func(*ciExecutionOptions){
		func(o *ciExecutionOptions) { o.id = "../parent" },
		func(o *ciExecutionOptions) { o.key = "" },
		func(o *ciExecutionOptions) { o.commit = "new-commit" },
		func(o *ciExecutionOptions) { o.profile = uuid.NewString() },
		func(o *ciExecutionOptions) { o.revision = uuid.NewString() },
		func(o *ciExecutionOptions) { o.binding = uuid.NewString() },
		func(o *ciExecutionOptions) { o.templates = ciTemplates{"test": uuid.NewString()} },
		func(o *ciExecutionOptions) { o.attempt = uuid.NewString() },
		func(o *ciExecutionOptions) { o.job = "test" },
	} {
		invalid := options
		change(&invalid)
		if _, _, _, _, err := ciExecutionRequest("rerun", invalid); err == nil {
			t.Fatal("rerun accepted missing identity or execution overrides")
		}
	}
}
