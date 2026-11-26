package main

import (
	"encoding/json"
	"strings"
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

func TestMCPTaskInputReviewAcceptsCanonicalGroupEndpoint(t *testing.T) {
	schema := map[string]any{"type": "object"}
	formHash, err := mcpFormDigest("Review", schema)
	if err != nil {
		t.Fatal(err)
	}
	review := mcpTaskInputReview{
		Version: 2, EndpointSHA256: mcpTaskReceiptEndpoint("https://reports.example.com/mcp"),
		TaskID: "task-id", Revision: strings.Repeat("a", 64),
		Forms: map[string]mcpReviewedForm{"approval": {
			Message: "Review", Schema: schema, FormSHA256: formHash,
		}},
	}
	body, err := encodeMCPTaskInputTemplate(review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeMCPTaskInputReview(body, "https://REPORTS.EXAMPLE.COM:443/mcp"); err != nil {
		t.Fatalf("equivalent endpoint failed: %v", err)
	}
	for _, endpoint := range []string{"https://reports.example.com:8443/mcp", "https://reports.example.com/other"} {
		if _, err := decodeMCPTaskInputReview(body, endpoint); err == nil {
			t.Fatalf("accepted a different endpoint %q", endpoint)
		}
	}
}
