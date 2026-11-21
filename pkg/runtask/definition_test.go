package runtask_test

import (
	"encoding/json"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtask"
	"testing"
)

func TestDefinitionBindingAndImmutability(t *testing.T) {
	request := api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x => x", InputSchema: json.RawMessage(`{"type":"object","required":["rows"]}`)}}
	normalized, meta, err := runtask.Normalize(request, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := age.GenerateX25519Identity()
	account, id := uuid.NewString(), uuid.NewString()
	blob, err := runtask.Seal([]*age.X25519Recipient{identity.Recipient()}, account, id, meta.Digest, normalized)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runtask.Open([]*age.X25519Identity{identity}, account, id, meta.Digest, blob)
	if err != nil || got.Definition.Source != request.Definition.Source {
		t.Fatal("round trip", err)
	}
	for _, binding := range [][3]string{{uuid.NewString(), id, meta.Digest}, {account, uuid.NewString(), meta.Digest}, {account, id, "bad-digest"}} {
		if _, err := runtask.Open([]*age.X25519Identity{identity}, binding[0], binding[1], binding[2], blob); err == nil {
			t.Fatal("accepted transplanted definition")
		}
	}
	request.Definition.Input = json.RawMessage(`{"secret":"customer"}`)
	if _, _, err := runtask.Normalize(request, api.PlanPro); err == nil {
		t.Fatal("retained invocation input")
	}
	request.Definition.Input = nil
	request.AgentEndpointIDs = []string{uuid.NewString()}
	_, changed, err := runtask.Normalize(request, api.PlanPro)
	if err != nil || changed.Digest == meta.Digest {
		t.Fatal("endpoint authority absent from digest", err)
	}
}

func TestStoredTaskFreezesDiskFittingOutputBudget(t *testing.T) {
	request := api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default x=>x", ArtifactStorage: api.ExecutionArtifactStored, OutputFiles: []string{"report.txt"}, Limits: &api.ExecutionLimitRequest{EphemeralDiskMB: 64}}}
	normalized, meta, err := runtask.Normalize(request, api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	// Publishing must not freeze a larger output allowance than a subsequent
	// invocation can stage beside the source and its JSON input.
	resolved, p := normalized.Definition.Resolve(api.PlanScale)
	if p != nil {
		t.Fatal(p)
	}
	available := int64(resolved.Limits.EphemeralDiskMB)*(1<<20) - api.ExecutionFileScratchReserveBytes - int64(resolved.SourceBytes()+len(`{"rows":3}`))
	if meta.Limits.MaxArtifactBytes <= 0 || meta.Limits.MaxArtifactBytes > available || resolved.Limits.MaxArtifactBytes != meta.Limits.MaxArtifactBytes {
		t.Fatal("unusable frozen output allowance", meta.Limits.MaxArtifactBytes, available)
	}
	request.Definition.Limits.MaxArtifactBytes = api.ExecutionFileHardMaxBytes
	if _, _, err := runtask.Normalize(request, api.PlanScale); err == nil {
		t.Fatal("explicit oversized output budget accepted")
	}
}
