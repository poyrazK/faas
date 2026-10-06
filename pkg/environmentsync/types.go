// Package environmentsync compiles and reconciles the customer environment
// contract. Planning is pure; customer intent is executed by apid, never by
// the Git fetcher or the runtime scheduler.
package environmentsync

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const APIVersion = "gregale.dev/environment/v1"

// Field identifies the smallest independently managed intent value. Atomic
// policy collections use one field; variable/runtime maps use one per key.
type Field struct {
	Resource string          `json:"resource"`
	Path     string          `json:"path"`
	Value    json.RawMessage `json:"value"`
}

func (f Field) Key() string { return f.Resource + "#" + f.Path }

type DesiredState struct {
	Definition api.EnvironmentDefinition `json:"definition"`
	Digest     string                    `json:"digest"`
	Fields     []Field                   `json:"fields"`
}

// ObservedState contains intent only, read at one database snapshot. Version
// is a durable intent version, not a timestamp or a runtime counter.
type ObservedState struct {
	Version     int64             `json:"version"`
	Fields      []Field           `json:"fields"`
	ResourceIDs map[string]string `json:"resource_ids"`
	Unsupported []string          `json:"unsupported,omitempty"`
}

type Ownership struct {
	Field
	Manager string `json:"manager"`
}

type Override struct {
	Resource  string    `json:"resource"`
	Path      string    `json:"path"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PlanOptions are internal authorization inputs, not user-supplied claims.
type PlanOptions struct {
	Manager    string
	Revision   string
	CommitSHA  string
	Generation int64
	Adopt      bool
	Prune      bool
	Now        time.Time
	Overrides  []Override
}

type Change = api.EnvironmentGitOpsChange

type Plan = api.EnvironmentGitOpsPlan
