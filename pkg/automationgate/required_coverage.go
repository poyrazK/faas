// adr: 1300
package automationgate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// CoveragePolicy version 1 requires exact saved-case names in the candidate.
// RequiredCases is a set: its order does not affect the canonical hash.
type CoveragePolicy struct {
	Version       int      `json:"version"`
	RequiredCases []string `json:"required_cases"`
}

func (p CoveragePolicy) Validate() error {
	if p.Version != 1 || len(p.RequiredCases) == 0 || len(p.RequiredCases) > api.AutomationLiveSuiteMaxCases {
		return errors.New("coverage policy requires version 1 and between 1 and 32 required cases")
	}
	seen := make(map[string]bool)
	for _, name := range p.RequiredCases {
		if !validName(name, api.AutomationTestCaseNameMaxBytes) || seen[name] {
			return errors.New("coverage policy requires distinct valid saved-case names")
		}
		seen[name] = true
	}
	return nil
}

// ParseCoveragePolicy rejects unknown fields, duplicate JSON keys and trailing data.
func ParseCoveragePolicy(data []byte) (CoveragePolicy, error) {
	var p CoveragePolicy
	if int64(len(data)) > api.AutomationLiveGatePolicyMaxBytes || !utf8.Valid(data) {
		return p, errors.New("invalid coverage policy encoding or size")
	}
	// Decode the object manually so duplicate fields cannot silently override policy.
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return p, errors.New("expected a coverage policy object")
	}
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return p, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return p, errors.New("duplicate policy field")
		}
		seen[name] = true
		switch name {
		case "version":
			err = d.Decode(&p.Version)
		case "required_cases":
			err = d.Decode(&p.RequiredCases)
		default:
			return p, errors.New("unknown policy field")
		}
		if err != nil {
			return p, err
		}
	}
	if _, err := d.Token(); err != nil {
		return p, err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return p, errors.New("expected one coverage policy object")
	}
	return p, p.Validate()
}

// Hash returns SHA-256 of canonical JSON, including the policy format version.
func (p CoveragePolicy) Hash() string {
	names := append([]string(nil), p.RequiredCases...)
	sort.Strings(names)
	canonical, _ := json.Marshal(CoveragePolicy{Version: p.Version, RequiredCases: names})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
