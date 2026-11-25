package flags

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// Evidence is application-reported behavior, attributed to the customer by the
// gateway. It is neither permission proof nor an exactly-once exposure counter.
type Evidence struct {
	Decision
	Used bool `json:"used"`
}

func DecodeEvidenceHeader(value string) (string, error) {
	if len(value) > api.FlagsMaxEvidenceBytes*2 {
		return "", fmt.Errorf("flag evidence too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return CanonicalEvidence(raw)
}
func CanonicalEvidence(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "[]", nil
	}
	if len(raw) > api.FlagsMaxEvidenceBytes {
		return "", fmt.Errorf("flag evidence too large")
	}
	var rows []Evidence
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rows); err != nil {
		return "", err
	}
	if dec.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("trailing flag evidence")
	}
	if len(rows) > api.FlagsMaxEvidencePerRequest {
		return "", fmt.Errorf("flag evidence count exceeded")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Flag] || validateDecision(r.Decision, true) != nil {
			return "", fmt.Errorf("invalid flag evidence")
		}
		seen[r.Flag] = true
	}
	if rows == nil {
		rows = []Evidence{}
	}
	slices.SortFunc(rows, func(a, b Evidence) int {
		if a.Flag < b.Flag {
			return -1
		}
		if a.Flag > b.Flag {
			return 1
		}
		return 0
	})
	out, err := json.Marshal(rows)
	return string(out), err
}

// MergeEvidence adds the gateway's own decisions (edge routing,
// ADR-20261124212701) to the canonical evidence the application reported. The
// application's evidence for a flag wins: it reports what the code did. The
// result is canonical; decisions past the evidence bound are dropped.
func MergeEvidence(canonical string, extra []Evidence) (string, error) {
	var rows []Evidence
	if canonical != "" {
		if err := json.Unmarshal([]byte(canonical), &rows); err != nil {
			return "", fmt.Errorf("merge flag evidence: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Flag] = true
	}
	for _, e := range extra {
		if seen[e.Flag] || len(rows) >= api.FlagsMaxEvidencePerRequest {
			continue
		}
		seen[e.Flag] = true
		rows = append(rows, e)
	}
	if rows == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return CanonicalEvidence(raw)
}
