package eventfilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Bound names the dimension of a filter that exceeded its cap.
type Bound string

const (
	BoundBytes Bound = "bytes"
	BoundDepth Bound = "depth"
)

// BoundError reports a filter over its size or nesting cap. Callers map it to
// a limit problem carrying Limit and Observed.
type BoundError struct {
	Bound    Bound
	Limit    int
	Observed int
}

func (e *BoundError) Error() string {
	if e.Bound == BoundDepth {
		return fmt.Sprintf("event: subscription filter nests %d levels deep; the limit is %d", e.Observed, e.Limit)
	}
	return fmt.Sprintf("event: subscription filter is %d bytes as compact JSON; the limit is %d", e.Observed, e.Limit)
}

// ValidateBoundedFilter applies ValidateFilter and caps the filter's compact
// JSON size and its object/array nesting depth (the top-level object is depth
// 1). Write paths that persist a subscription use it; evaluation of an
// already stored filter uses ValidateFilter so rows that predate the caps keep
// matching. The caps are passed in because pkg/api imports this package.
func ValidateBoundedFilter(filter json.RawMessage, maxBytes, maxDepth int) error {
	trimmed := bytes.TrimSpace(filter)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return fmt.Errorf("event: decode subscription filter: %w", err)
	}
	if compact.Len() > maxBytes {
		return &BoundError{Bound: BoundBytes, Limit: maxBytes, Observed: compact.Len()}
	}
	depth, err := filterDepth(compact.Bytes())
	if err != nil {
		return err
	}
	if depth > maxDepth {
		return &BoundError{Bound: BoundDepth, Limit: maxDepth, Observed: depth}
	}
	return ValidateFilter(compact.Bytes())
}

// filterDepth walks the token stream, so nesting is measured without
// recursion.
func filterDepth(filter []byte) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(filter))
	depth, deepest := 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return deepest, nil
		}
		if err != nil {
			return 0, fmt.Errorf("event: decode subscription filter: %w", err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delim {
		case '{', '[':
			depth++
			deepest = max(deepest, depth)
		case '}', ']':
			depth--
		}
	}
}
