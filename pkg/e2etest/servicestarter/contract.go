// Package servicestarter pins the isolated managed HTTPS guest acceptance.
package servicestarter

import (
	_ "embed"
	"encoding/json"
	"regexp"
)

const IdentityPrefix = "GREGALE_SERVICE_GUEST_IDENTITY "

//go:embed contract.json
var contractJSON []byte

type Contract struct {
	Version   int      `json:"version"`
	Package   string   `json:"package"`
	Root      string   `json:"root"`
	Template  string   `json:"template"`
	Runtime   string   `json:"runtime"`
	Phases    []string `json:"phases"`
	Artifacts []string `json:"artifacts"`
}

func SuiteContract() Contract {
	var c Contract
	if json.Unmarshal(contractJSON, &c) != nil {
		panic("invalid managed HTTPS acceptance contract")
	}
	return c
}

type RuntimeManifest struct {
	Version        int               `json:"version"`
	SourceRevision string            `json:"source_revision"`
	Template       string            `json:"template"`
	Runtime        string            `json:"runtime"`
	Artifacts      map[string]string `json:"artifacts"`
}

func (m RuntimeManifest) Valid() bool {
	c := SuiteContract()
	if m.Version != c.Version || m.Template != c.Template || m.Runtime != c.Runtime ||
		!regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(m.SourceRevision) || len(m.Artifacts) != len(c.Artifacts) {
		return false
	}
	for _, name := range c.Artifacts {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(m.Artifacts[name]) {
			return false
		}
	}
	return true
}
