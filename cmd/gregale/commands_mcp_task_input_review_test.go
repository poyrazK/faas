package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPFormDigestPreservesSchemaNumberPrecision(t *testing.T) {
	inputs := []string{
		`{"method":"elicitation/create","params":{"message":"Review a number","requestedSchema":{"type":"object","properties":{"count":{"const":9007199254740992}}}}}`,
		`{"method":"elicitation/create","params":{"message":"Review a number","requestedSchema":{"type":"object","properties":{"count":{"const":9007199254740993}}}}}`,
		`{"method":"elicitation/create","params":{"message":"Review a number","requestedSchema":{"properties":{"count":{"const":9007199254740993.0}},"type":"object"}}}`,
	}
	digests := make([]string, len(inputs))
	for i, input := range inputs {
		var request mcphosting.TaskInputRequest
		if err := json.Unmarshal([]byte(input), &request); err != nil {
			t.Fatal(err)
		}
		digest, err := mcpFormDigest(request.Params.Message, request.Params.RequestedSchema)
		if err != nil {
			t.Fatal(err)
		}
		digests[i] = digest
	}
	if digests[0] == digests[1] || digests[1] != digests[2] {
		t.Fatalf("unexpected digests for distinct and equivalent forms: %v", digests)
	}
}
