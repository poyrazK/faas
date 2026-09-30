// adr: 126, 375
package openapiimport

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// JSONDocument returns the document representation stored in JSONB. JSON
// bytes keep their original numbers and formatting; YAML uses the same value
// parser as ValidateImport. Upload size/hash metadata can retain the input.
func JSONDocument(body []byte) ([]byte, error) {
	if json.Valid(body) {
		return body, nil
	}
	var document any
	if err := yaml.Unmarshal(body, &document); err != nil {
		return nil, &ValidationError{Reason: fmt.Sprintf("invalid JSON or YAML: %s", err)}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, &ValidationError{Reason: fmt.Sprintf("YAML document is not representable as JSON: %s", err)}
	}
	return encoded, nil
}
