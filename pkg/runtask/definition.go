// Package runtask validates and binds immutable source Run tool definitions.
package runtask

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executioncontract"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const DefinitionNamespace = "gregale.run-task.definition.v1"

var taskName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func ValidRef(name string, version int) bool {
	return len(name) > 0 && len(name) <= api.ExecutionTaskNameMaxBytes && taskName.MatchString(name) && version > 0 && version <= api.ExecutionTaskVersionMax
}

// Normalize freezes plan defaults, validates schemas without an invocation input,
// and excludes ephemeral data from the persistent definition.
func Normalize(r api.PublishExecutionTaskRequest, plan api.Plan) (api.PublishExecutionTaskRequest, api.ExecutionTaskVersionResponse, error) {
	if !ValidRef(r.Name, r.Version) {
		return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("name must be a lower-case task name and version must be a positive bounded integer")
	}
	d := r.Definition
	if d.CustomerRef != "" || len(d.Input) > 0 || len(d.InputFiles) > 0 || len(d.ArtifactInputs) > 0 || d.WorkflowID != "" || d.StepLabel != "" {
		return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("task definitions cannot retain invocation input, file references or workflow identity")
	}
	for _, schema := range []json.RawMessage{d.InputSchema, d.ResultSchema} {
		if _, err := executioncontract.Compile(schema); err != nil {
			return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("task schemas must be bounded local JSON Schema contracts")
		}
	}
	resolved, problem := d.Resolve(plan)
	if problem != nil {
		return r, api.ExecutionTaskVersionResponse{}, problem
	}
	if d.ArtifactStorage == api.ExecutionArtifactStored && len(d.OutputFiles) > 0 {
		planLimits, _ := plan.ExecutionLimits()
		available := int64(resolved.Limits.EphemeralDiskMB)*(1<<20) - api.ExecutionFileScratchReserveBytes - int64(resolved.SourceBytes()+planLimits.MaxInputBytes)
		if available <= 0 {
			return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("task scratch disk cannot fit its source and input envelope")
		}
		if d.Limits != nil && d.Limits.MaxArtifactBytes != 0 {
			if resolved.Limits.MaxArtifactBytes > available {
				return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("task output budget cannot fit its scratch disk; increase ephemeral_disk_mb or reduce max_artifact_bytes")
			}
		} else {
			// Leave room for dynamic file inputs as well as outputs. The published
			// metadata exposes the frozen cap; an invocation still checks exact sizes.
			resolved.Limits.MaxArtifactBytes = min(resolved.Limits.MaxArtifactBytes, available/api.ExecutionTaskDefaultOutputDiskDivisor)
		}
	}
	ids, err := api.NormalizeExecutionIntegrationIDs(d.IntegrationIDs)
	if err != nil {
		return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("invalid task integration IDs")
	}
	if len(r.AgentEndpointIDs) > api.ExecutionTaskAgentEndpointMax {
		return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("too many task agent endpoints")
	}
	endpoints := make([]string, 0, len(r.AgentEndpointIDs))
	seen := map[string]bool{}
	for _, raw := range r.AgentEndpointIDs {
		id, e := uuid.Parse(raw)
		if e != nil || seen[id.String()] {
			return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("agent_endpoint_ids must be distinct UUIDs")
		}
		seen[id.String()] = true
		endpoints = append(endpoints, id.String())
	}
	sort.Strings(endpoints)
	d.Profile = resolved.Profile.Normalized()
	d.Network = &resolved.Network
	d.Limits = &api.ExecutionLimitRequest{Class: resolved.Limits.Class.Normalized(), TimeoutMS: resolved.Limits.TimeoutMS, MemoryMB: resolved.Limits.MemoryMB, CPUMillicores: resolved.Limits.CPUMillicores, EphemeralDiskMB: resolved.Limits.EphemeralDiskMB, MaxOutputBytes: resolved.Limits.MaxOutputBytes, MaxArtifactBytes: resolved.Limits.MaxArtifactBytes}
	d.IntegrationIDs = ids
	d.OutputFiles = resolved.OutputFiles
	if d.ArtifactStorage == "" {
		d.ArtifactStorage = api.ExecutionArtifactInline
	}
	r.Definition = d
	r.AgentEndpointIDs = endpoints
	canonical, err := Canonical(r)
	if err != nil || len(canonical) > api.ExecutionTaskDefinitionMaxBytes {
		return r, api.ExecutionTaskVersionResponse{}, api.ErrValidation("task definition exceeds its retained byte limit")
	}
	sum := sha256.Sum256(canonical)
	meta := api.ExecutionTaskVersionResponse{ExecutionTaskRef: api.ExecutionTaskRef{Name: r.Name, Version: r.Version, Digest: hex.EncodeToString(sum[:])}, Runtime: d.Runtime, Profile: d.Profile, EnvironmentID: d.EnvironmentID, Limits: resolved.Limits, InputSchema: d.InputSchema, ResultSchema: d.ResultSchema, IntegrationIDs: ids, OutputFiles: d.OutputFiles, ArtifactStorage: d.ArtifactStorage, AgentEndpointIDs: endpoints, Status: "active"}
	return r, meta, nil
}
func Canonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

type envelope struct {
	AccountID string                          `json:"account_id"`
	VersionID string                          `json:"version_id"`
	Digest    string                          `json:"digest"`
	Task      api.PublishExecutionTaskRequest `json:"task"`
}

func Seal(recipients []*age.X25519Recipient, accountID, versionID, digest string, task api.PublishExecutionTaskRequest) ([]byte, error) {
	raw, err := json.Marshal(envelope{accountID, versionID, digest, task})
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	return secretbox.SealBytesTo(recipients, DefinitionNamespace, raw, api.ExecutionTaskEnvelopeMaxBytes)
}
func Open(identities []*age.X25519Identity, accountID, versionID, digest string, blob []byte) (api.PublishExecutionTaskRequest, error) {
	ns, raw, err := secretbox.OpenBytesMulti(identities, blob)
	if err != nil {
		return api.PublishExecutionTaskRequest{}, errors.New("task definition could not be opened")
	}
	defer clear(raw)
	var e envelope
	if ns != DefinitionNamespace || len(raw) > api.ExecutionTaskEnvelopeMaxBytes || json.Unmarshal(raw, &e) != nil || e.AccountID != accountID || e.VersionID != versionID || e.Digest != digest {
		return api.PublishExecutionTaskRequest{}, errors.New("task definition binding is invalid")
	}
	canonical, err := Canonical(e.Task)
	sum := sha256.Sum256(canonical)
	if err != nil || hex.EncodeToString(sum[:]) != digest {
		return api.PublishExecutionTaskRequest{}, errors.New("task definition digest is invalid")
	}
	return e.Task, nil
}
