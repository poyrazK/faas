package api

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

const ManagedServiceProbeContract = "managed-https-v1"

// Reuse the oldest reserved probe handler with a distinct argument shape.
// Existing guests reject its third argument instead of executing a customer
// binary named after a new, unknown operation.
const AppTaskManagedServiceBindingProbeCommand = AppTaskServiceBindingProbeCommand

// ManagedServiceProbeSpec is selected by the server from the active frozen
// graph. It contains no credentials, variable values or application request.
type ManagedServiceProbeSpec struct {
	Version            int    `json:"version"`
	CallerDeploymentID string `json:"caller_deployment_id"`
	Scope              string `json:"scope"`
	Service            string `json:"service"`
	Binding            string `json:"binding"`
	TargetAppID        string `json:"target_app_id"`
	TargetDeploymentID string `json:"target_deployment_id"`
	ReleaseID          string `json:"release_id"`
	ContextRevision    string `json:"context_revision"`
}

func (s ManagedServiceProbeSpec) Valid() bool {
	if s.Version != 1 || ValidateScope(s.Scope) != nil || ValidateEnvKey(s.Binding) != nil || !ValidAppSlug(s.Service) {
		return false
	}
	for _, id := range []string{s.CallerDeploymentID, s.TargetAppID, s.TargetDeploymentID, s.ReleaseID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return false
		}
	}
	raw, err := hex.DecodeString(s.ContextRevision)
	return err == nil && len(raw) == 32 && s.ContextRevision == strings.ToLower(s.ContextRevision)
}

func (s ManagedServiceProbeSpec) Command() []string {
	raw, _ := json.Marshal(s)
	return []string{AppTaskManagedServiceBindingProbeCommand, s.Service, string(raw)}
}

func ParseManagedServiceProbeCommand(command []string, shell bool) (ManagedServiceProbeSpec, bool) {
	var spec ManagedServiceProbeSpec
	if shell || len(command) != 3 || command[0] != AppTaskManagedServiceBindingProbeCommand || len(command[2]) > 4096 {
		return spec, false
	}
	decoder := json.NewDecoder(strings.NewReader(command[2]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&spec) != nil || !spec.Valid() || command[1] != spec.Service {
		return ManagedServiceProbeSpec{}, false
	}
	// Require the exact canonical encoding, rejecting trailing or duplicate fields.
	return spec, spec.Command()[2] == command[2]
}

func ManagedServiceProbeForItem(item AppBindingInventoryItem) (ManagedServiceProbeSpec, bool) {
	c := item.ServiceContext
	if item.Type != BindingTypeService || c == nil || c.Source != "environment_release" || !c.VerificationSupported || c.VerificationReason != "" || c.VerificationContract != ManagedServiceProbeContract || c.RoutingStatus != "resolved" || item.Transport != "https" {
		return ManagedServiceProbeSpec{}, false
	}
	spec := ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: c.CallerDeploymentID, Scope: item.Scope, Service: item.Name, Binding: item.Binding,
		TargetAppID: c.TargetAppID, TargetDeploymentID: item.ServiceTargetDeploymentID, ReleaseID: c.ReleaseID, ContextRevision: c.Revision}
	return spec, spec.Valid()
}
