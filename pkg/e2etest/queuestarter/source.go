// Package queuestarter describes the unchanged shipped queue worker acceptance.
package queuestarter

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

const IdentityPrefix = "GREGALE_QUEUE_GUEST_IDENTITY "

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
		panic("invalid queue acceptance contract")
	}
	return c
}

// Source retains every shipped byte, including the handler and manifest.
func Source() (map[string]string, error) {
	files := map[string]string{}
	root := SuiteContract().Template
	err := fs.WalkDir(templates.FS, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(templates.FS, path)
		if err == nil {
			files[strings.TrimPrefix(path, root+"/")] = string(content)
		}
		return err
	})
	return files, err
}

// BindingRequest reconciles the shipped manifest through the ordinary queue API.
// Copy retry fields explicitly: the manifest uses YAML tags, not API JSON tags.
func BindingRequest() (api.CreateQueueBindingRequest, error) {
	files, err := Source()
	if err != nil {
		return api.CreateQueueBindingRequest{}, err
	}
	manifest, err := gregalemanifest.ParseBytes([]byte(files["gregale.yaml"]))
	if err != nil {
		return api.CreateQueueBindingRequest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return api.CreateQueueBindingRequest{}, err
	}
	if len(manifest.QueueBindings) != 1 {
		return api.CreateQueueBindingRequest{}, errors.New("queue acceptance requires one shipped binding")
	}
	binding := manifest.QueueBindings[0]
	request := api.CreateQueueBindingRequest{Name: binding.Name, QueueName: binding.QueueName, Mode: binding.Mode, WorkloadClass: binding.WorkloadClass, Enabled: binding.Enabled, MaxConcurrency: binding.MaxConcurrency}
	if retry := binding.RetryPolicy; retry != nil {
		request.RetryPolicy = &api.RetryPolicyDTO{MaxAttempts: retry.MaxAttempts, BaseSeconds: retry.BaseSeconds, MaxSeconds: retry.MaxSeconds, JitterSeconds: retry.JitterSeconds}
	}
	return request, nil
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
