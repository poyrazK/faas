package productstandards

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAsyncAPIOfficialSchema(t *testing.T) {
	body, err := os.ReadFile("../../api/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAsyncAPI(body); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncAPIOfficialSchemaRejectsOpenAPISecurity(t *testing.T) {
	body, err := os.ReadFile("../../api/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	operation := object(t, object(t, document, "operations"), "receiveInternalEventPublish")
	operation["security"] = []any{map[string]any{"bearerAuth": []any{}}}
	body, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAsyncAPI(body); err == nil {
		t.Fatal("official schema accepted an OpenAPI security requirement in AsyncAPI 3")
	}
}
