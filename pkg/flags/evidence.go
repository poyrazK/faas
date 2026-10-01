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
		_, booleanValue := r.Value.(bool)
		_, variantValue := r.Value.(string)
		validType := (r.Type == "" || r.Type == "boolean") && booleanValue || r.Type == "variant" && variantValue
		if !ValidKey(r.Flag) || seen[r.Flag] || r.ConfigVersion < 0 || r.ConfigVersion > api.FlagsMaxConfigVersion || r.RuleID != "" && !ValidKey(r.RuleID) || r.Bucket != nil && (*r.Bucket < 0 || *r.Bucket >= 10000) || r.RolloutBucket != nil && (*r.RolloutBucket < 0 || *r.RolloutBucket >= 10000) || !validType {
			return "", fmt.Errorf("invalid flag evidence")
		}
		seen[r.Flag] = true
		switch r.Reason {
		case "flag_missing", "default", "disabled", "customer_missing", "rule_match", "configuration_stale", "type_mismatch":
		default:
			return "", fmt.Errorf("invalid flag decision reason")
		}
		if r.Source != "fallback" && r.Source != "configuration" {
			return "", fmt.Errorf("invalid flag evidence source")
		}
		if (r.Reason == "flag_missing" || r.Reason == "configuration_stale" || r.Reason == "type_mismatch") != (r.Source == "fallback") {
			return "", fmt.Errorf("flag evidence source does not match its decision reason")
		}
		if r.Reason == "rule_match" && r.RuleID == "" || r.Reason != "rule_match" && (r.RuleID != "" || r.Bucket != nil || r.RolloutBucket != nil) {
			return "", fmt.Errorf("invalid flag rule evidence")
		}
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
