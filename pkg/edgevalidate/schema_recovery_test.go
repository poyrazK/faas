package edgevalidate_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

func TestLoadedRuleSurvivesSchemaEvictionAndReset(t *testing.T) {
	manager := edgevalidate.NewManager(nil)
	source := []byte(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	compiled, err := manager.CompileSchema(source, true)
	if err != nil {
		t.Fatal(err)
	}
	rule := &edgevalidate.Rule{SchemaDigest: compiled.Digest, Schema: source, RejectUnknownFields: true}
	for i := 0; i <= edgevalidate.MaxCompiledSchemas; i++ {
		if _, err := manager.CompileSchema([]byte(fmt.Sprintf(`{"const":%d}`, i)), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, present := manager.Cache().Get(compiled.Digest); present {
		t.Fatal("fixture did not evict the loaded schema")
	}
	for _, reset := range []bool{false, true} {
		if reset {
			manager.Cache().Reset()
		}
		result, err := manager.Validate(context.Background(), &edgevalidate.In{Body: []byte(`{"name":"Ada"}`)}, rule)
		if err != nil || !result.OK {
			t.Fatalf("loaded rule failed after eviction/reset: result=%+v err=%v", result, err)
		}
		result, err = manager.Validate(context.Background(), &edgevalidate.In{Body: []byte(`{"name":"Ada","extra":1}`)}, rule)
		if err != nil || result.OK {
			t.Fatalf("recovery lost closed-schema policy: result=%+v err=%v", result, err)
		}
	}
	manager.Cache().Reset()
	rule.Schema = []byte(`true`)
	if _, err := manager.Validate(context.Background(), &edgevalidate.In{Body: []byte(`{}`)}, rule); !errors.Is(err, edgevalidate.ErrSchemaInvalid) {
		t.Fatalf("mismatched recovery source was accepted: %v", err)
	}
}

func TestInlineSchemaReferencesCannotReadExternalResources(t *testing.T) {
	for _, source := range []string{
		`{"$ref":"file:/does-not-exist.json"}`,
		`{"$dynamicRef":"file:///does-not-exist.json"}`,
		`{"\u0024ref":"file:/does-not-exist.json"}`,
		`{"$ref":"../schemas/common.json"}`,
		`{"$id":"file:/does-not-exist.json"}`,
		`{"$recursiveRef":"file:/does-not-exist.json"}`,
	} {
		if _, err := edgevalidate.Compile([]byte(source), false); !errors.Is(err, edgevalidate.ErrSchemaExternalRef) {
			t.Errorf("external resource accepted or misclassified: %s: %v", source, err)
		}
	}
	// Reference-shaped application data is not a schema reference.
	if _, err := edgevalidate.Compile([]byte(`{"const":{"$ref":"https://example.test/value"}}`), false); err != nil {
		t.Fatalf("application data rejected: %v", err)
	}
}
