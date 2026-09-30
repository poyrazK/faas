package productstandards

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/asyncapi/3.0.0.json
var asyncAPISchemaJSON []byte

var compiledAsyncAPISchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	var document map[string]any
	if err := json.Unmarshal(asyncAPISchemaJSON, &document); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineSchemaLoader{})
	url := document["$id"].(string)
	if err := compiler.AddResource(url, document); err != nil {
		return nil, err
	}
	return compiler.Compile(url)
})

type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("schema resource %q is not vendored", url)
}

// ValidateAsyncAPI checks a published YAML document against the unmodified,
// pinned official AsyncAPI schema. Validation never downloads schemas.
func ValidateAsyncAPI(body []byte) error {
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("parse AsyncAPI: %w", err)
	}
	// Normalize YAML numbers and timestamp values to JSON's type system.
	normalized, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("normalize AsyncAPI: %w", err)
	}
	var value any
	if err := json.Unmarshal(normalized, &value); err != nil {
		return fmt.Errorf("decode AsyncAPI: %w", err)
	}
	schema, err := compiledAsyncAPISchema()
	if err != nil {
		return fmt.Errorf("compile official AsyncAPI schema: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate official AsyncAPI schema: %w", err)
	}
	return nil
}
