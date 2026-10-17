package promotion

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Validate rejects incomplete or unredacted responses before a CLI prints them.
func Validate(out api.PreviewEnvironmentGitPromotionResponse) error {
	if !out.ReadOnly || !out.ValuesRedacted || !out.RequiresTargetQualification || !api.ValidProjectSlug(out.Project) ||
		!api.ValidProjectEnvironmentSlug(out.FromEnvironment) || !api.ValidProjectEnvironmentSlug(out.ToEnvironment) || out.FromEnvironment == out.ToEnvironment ||
		out.Origin.SourceID == "" || out.Target.SourceID == "" || out.Origin.SourceID == out.Target.SourceID || out.Origin.RevisionID == "" {
		return fmt.Errorf("invalid promotion scope or preview boundary")
	}
	for _, digest := range []string{out.PlanHash, out.Origin.DefinitionDigest, out.Target.DefinitionDigest, out.Target.ProposedDigest, out.TargetPlan.Hash} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 || strings.ToLower(digest) != digest {
			return fmt.Errorf("invalid promotion digest")
		}
	}
	if out.TargetPlan.Manager != out.Target.SourceID || out.TargetPlan.Revision != "candidate:"+out.Target.CommitSHA || out.TargetPlan.CommitSHA != out.Target.CommitSHA || out.TargetPlan.DesiredDigest != out.Target.ProposedDigest || out.TargetPlan.Generation != out.Target.Generation || out.TargetPlan.ObservedVersion != out.Target.IntentVersion {
		return fmt.Errorf("promotion target plan identity mismatch")
	}
	for _, change := range out.TargetPlan.Changes {
		if len(change.Before) != 0 || len(change.After) != 0 {
			return fmt.Errorf("promotion values were not redacted")
		}
	}
	for _, artifact := range out.Artifacts {
		if hasBuildArtifact(artifact) && !validBuildArtifact(artifact) {
			return fmt.Errorf("invalid promotion build artifact identity")
		}
		if artifact.ImageDigest != "" && !validImageIdentity(artifact.ImageDigest) {
			return fmt.Errorf("invalid promotion artifact identity")
		}
	}
	for _, change := range out.Changes {
		if !validPromotionSource(change.Before) || !validPromotionSource(change.After) || change.Before.Kind != change.After.Kind ||
			change.After.Kind != "image" && (!api.ValidEnvironmentBuildArtifactReference(change.After.BuildArtifactID, change.After.BuildArtifactHash) || sourceRecipe(change.Before) != sourceRecipe(change.After)) {
			return fmt.Errorf("invalid promotion source")
		}
	}
	expected := out.PlanHash
	out.PlanHash = ""
	verified, err := Finish(out, out.TargetPlan)
	if err != nil || verified.PlanHash != expected {
		return fmt.Errorf("promotion plan hash mismatch")
	}
	return nil
}

type Operation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// Patch is an RFC 6902 proposal envelope. BaseDefinitionDigest must be checked
// against the parsed manifest before applying Operations. It grants no authority.
type Patch struct {
	ReadOnly             bool        `json:"read_only"`
	PlanHash             string      `json:"plan_hash"`
	BaseCommitSHA        string      `json:"base_commit_sha"`
	BaseDefinitionDigest string      `json:"base_definition_digest"`
	ProposedDigest       string      `json:"proposed_digest"`
	ManifestPath         string      `json:"manifest_path"`
	Operations           []Operation `json:"operations"`
}

func ExportPatch(out api.PreviewEnvironmentGitPromotionResponse) (Patch, error) {
	var patch Patch
	if err := Validate(out); err != nil {
		return patch, err
	}
	if len(out.BlockingReasons) != 0 {
		return patch, fmt.Errorf("promotion is blocked; inspect the JSON plan")
	}
	patch = Patch{ReadOnly: true, PlanHash: out.PlanHash, BaseCommitSHA: out.Target.CommitSHA, BaseDefinitionDigest: out.Target.DefinitionDigest,
		ProposedDigest: out.Target.ProposedDigest, ManifestPath: out.Target.ManifestPath,
		Operations: []Operation{{"test", "/project", out.Project}, {"test", "/environment", out.ToEnvironment}}}
	for _, change := range out.Changes {
		name, ok := strings.CutPrefix(change.Resource, "workload/")
		if !ok || !api.ValidAppSlug(name) || !validPromotionSource(change.Before) || !validPromotionSource(change.After) {
			return Patch{}, fmt.Errorf("unsupported promotion source change")
		}
		path := "/workloads/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name) + "/source"
		patch.Operations = append(patch.Operations, Operation{"test", path, change.Before}, Operation{"replace", path, change.After})
	}
	return patch, nil
}

// ExportManifest checks the full local base digest, changes only source
// contracts and returns canonical JSON (also valid YAML). No file is written.
func ExportManifest(out api.PreviewEnvironmentGitPromotionResponse, definition api.EnvironmentDefinition) ([]byte, error) {
	if _, err := ExportPatch(out); err != nil {
		return nil, err
	}
	base, err := environmentsync.Compile(definition)
	if err != nil || base.Digest != out.Target.DefinitionDigest || definition.Project != out.Project || definition.Environment != out.ToEnvironment {
		return nil, fmt.Errorf("local target manifest changed; request a fresh promotion plan")
	}
	for _, change := range out.Changes {
		name := strings.TrimPrefix(change.Resource, "workload/")
		workload, exists := base.Definition.Workloads[name]
		if !exists || workload.Source == nil || *workload.Source != change.Before {
			return nil, fmt.Errorf("local workload source changed")
		}
		after := change.After
		workload.Source = &after
		base.Definition.Workloads[name] = workload
	}
	proposed, err := environmentsync.Compile(base.Definition)
	if err != nil || proposed.Digest != out.Target.ProposedDigest {
		return nil, fmt.Errorf("proposed manifest digest mismatch")
	}
	encoded, err := json.MarshalIndent(proposed.Definition, "", "  ")
	return append(encoded, '\n'), err
}

func validPromotionSource(source api.EnvironmentWorkloadSource) bool {
	if source.Kind == "image" {
		return api.ValidDeploymentImage(source.Image) && source.BuildArtifactID == "" && source.BuildArtifactHash == ""
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "validation", Environment: "validation", Workloads: map[string]api.EnvironmentWorkload{"app": {Source: &source}}})
	return err == nil && *desired.Definition.Workloads["app"].Source == source
}
