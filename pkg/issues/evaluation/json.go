package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// DecodeCorpus accepts independently curated, reviewed cases. It does not
// export incidents or infer labels. Reports never echo malformed JSON values.
func DecodeCorpus(raw []byte) ([]Case, error) {
	var cases []Case
	if err := decodeStrict(raw, MaxCorpusBytes, &cases); err != nil {
		return nil, fmt.Errorf("invalid grouping corpus: %w", err)
	}
	return cases, nil
}

func DecodeBaseline(raw []byte) (Baseline, error) {
	var baseline Baseline
	if err := decodeStrict(raw, api.IssueGroupingEvaluationBaselineMaxBytes, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("invalid grouping baseline: %w", err)
	}
	return baseline, nil
}

func decodeStrict(raw []byte, bound int, target any) error {
	if len(raw) > bound {
		return fmt.Errorf("document exceeds byte bound")
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("document must be UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := uniqueKeys(decoder, 0); err != nil {
		return fmt.Errorf("duplicate keys, excessive nesting or malformed JSON")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("unsupported fields or malformed JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one JSON document")
	}
	return nil
}

func uniqueKeys(decoder *json.Decoder, depth int) error {
	if depth > api.IssueGroupingEvaluationJSONDepthMax {
		return fmt.Errorf("excessive nesting")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			name, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := name.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate key")
			}
			seen[key] = true
		}
		if err := uniqueKeys(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
