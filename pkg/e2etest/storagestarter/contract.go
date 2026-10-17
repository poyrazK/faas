package storagestarter

import (
	_ "embed"
	"encoding/json"
	"regexp"
)

const IdentityPrefix = "GREGALE_STORAGE_GUEST_IDENTITY "

//go:embed contract.json
var contractJSON []byte

type Contract struct {
	Version   int       `json:"version"`
	Package   string    `json:"package"`
	Root      string    `json:"root"`
	Starters  []Starter `json:"starters"`
	Phases    []string  `json:"phases"`
	Artifacts []string  `json:"artifacts"`
}

type Starter struct {
	Template       string `json:"template"`
	Runtime        string `json:"runtime"`
	AppArtifact    string `json:"app_artifact"`
	SourceArtifact string `json:"source_artifact"`
	TrustEnv       string `json:"trust_env"`
}

func StarterForTemplate(template string) (Starter, bool) {
	for _, starter := range SuiteContract().Starters {
		if starter.Template == template {
			return starter, true
		}
	}
	return Starter{}, false
}

func SuiteContract() Contract {
	var result Contract
	if err := json.Unmarshal(contractJSON, &result); err != nil {
		panic("invalid storage acceptance contract")
	}
	return result
}

// RuntimeManifest binds this test's actual execution bundle to a clean revision.
// These hashes identify acceptance binaries, not differently tagged releases.
type RuntimeManifest struct {
	Version        int               `json:"version"`
	SourceRevision string            `json:"source_revision"`
	Template       string            `json:"template"`
	Runtime        string            `json:"runtime"`
	Artifacts      map[string]string `json:"artifacts"`
}

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (m RuntimeManifest) Valid() bool {
	contract := SuiteContract()
	starter, ok := StarterForTemplate(m.Template)
	if !ok || m.Runtime != starter.Runtime || m.Version != contract.Version || !revisionPattern.MatchString(m.SourceRevision) || len(m.Artifacts) != len(contract.Artifacts)+2 {
		return false
	}
	for _, name := range append(contract.Artifacts, starter.AppArtifact, starter.SourceArtifact) {
		if !digestPattern.MatchString(m.Artifacts[name]) {
			return false
		}
	}
	return true
}
