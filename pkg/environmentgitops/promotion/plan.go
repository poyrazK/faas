// Package promotion prepares read-only, reviewable environment source changes.
package promotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/diagnostics"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type Input struct {
	From, To     api.EnvironmentGitSource
	Revision     api.EnvironmentDesiredRevision
	TargetCommit string
	Target       environmentsync.DesiredState
	Evidence     *api.EnvironmentWorkloadActivationEvidence
	ReleaseID    string
	Artifacts    []api.EnvironmentGitPromotionArtifact
	Now          time.Time
}

func Prepare(in Input) (api.PreviewEnvironmentGitPromotionResponse, environmentsync.DesiredState, error) {
	var out api.PreviewEnvironmentGitPromotionResponse
	if in.From.ID == "" || in.To.ID == "" || in.From.AccountID == "" || in.From.ProjectID == "" || in.From.EnvironmentID == "" || in.To.EnvironmentID == "" || in.From.ID == in.To.ID || in.From.AccountID != in.To.AccountID || in.From.ProjectID != in.To.ProjectID || in.From.EnvironmentID == in.To.EnvironmentID || in.From.EnvironmentSlug == in.To.EnvironmentSlug {
		return out, environmentsync.DesiredState{}, fmt.Errorf("promotion requires two scoped sources in the same project")
	}
	var definition api.EnvironmentDefinition
	if json.Unmarshal(in.Revision.Definition, &definition) != nil {
		return out, environmentsync.DesiredState{}, fmt.Errorf("invalid approved definition")
	}
	from, err := environmentsync.Compile(definition)
	if err != nil || from.Digest != in.Revision.Digest || in.Revision.SourceID != in.From.ID || in.Revision.ID != in.From.ApprovedRevisionID || definition.Project != in.Target.Definition.Project || definition.Environment != in.From.EnvironmentSlug || in.Target.Definition.Environment != in.To.EnvironmentSlug {
		return out, environmentsync.DesiredState{}, fmt.Errorf("promotion definition identity mismatch")
	}
	out = api.PreviewEnvironmentGitPromotionResponse{Project: definition.Project, FromEnvironment: in.From.EnvironmentSlug, ToEnvironment: in.To.EnvironmentSlug,
		ObservedAt: in.Now.UTC(), ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true,
		Origin: api.EnvironmentGitPromotionOrigin{SourceID: in.From.ID, RevisionID: in.Revision.ID, CommitSHA: in.Revision.CommitSHA, DefinitionDigest: from.Digest,
			Generation: in.From.Generation, IntentVersion: in.From.IntentVersion, ReleaseSetID: in.ReleaseID, Provider: provider(in.From), RepositoryID: in.From.Spec.RepositoryID},
		Target: api.EnvironmentGitPromotionTarget{SourceID: in.To.ID, Generation: in.To.Generation, IntentVersion: in.To.IntentVersion, CommitSHA: in.TargetCommit,
			DefinitionDigest: in.Target.Digest, ManifestPath: in.To.Spec.ManifestPath},
		Artifacts: slices.Clone(in.Artifacts), Changes: []api.EnvironmentGitPromotionChange{}, BlockingReasons: []string{}}
	if out.Artifacts == nil {
		out.Artifacts = []api.EnvironmentGitPromotionArtifact{}
	}
	if provider(in.From) != provider(in.To) || in.From.Spec.RepositoryID != in.To.Spec.RepositoryID || in.From.Spec.InstallationID != in.To.Spec.InstallationID || in.From.Spec.Repository != in.To.Spec.Repository {
		out.BlockingReasons = append(out.BlockingReasons, "promotion_repository_identity_mismatch")
	}
	for _, source := range []api.EnvironmentGitSource{in.From, in.To} {
		if source.Detached || source.Suspended {
			out.BlockingReasons = append(out.BlockingReasons, "promotion_source_inactive")
		}
		assessment := diagnostics.Assess(api.EnvironmentGitOpsStatusResponse{Source: source}, in.Now)
		if assessment.SourceFreshness != "fresh" || source.SourceErrorCode != "" {
			out.BlockingReasons = append(out.BlockingReasons, "promotion_source_unverified")
		}
	}
	e := in.Evidence
	if e != nil {
		out.Origin.GraphID = e.GraphID
	}
	if e == nil || e.GraphID == "" || e.PlanHash == "" || e.GraphErrorCode != "" || e.SourceID != in.From.ID || e.EnvironmentID != in.From.EnvironmentID || e.RevisionID != in.Revision.ID || e.DefinitionDigest != from.Digest || e.Generation != in.From.Generation || e.IntentVersion != in.From.IntentVersion || !e.ArtifactsPrepared || !e.Qualified || !e.Activated || !e.Serving || len(e.BlockingReasons) != 0 || in.ReleaseID == "" {
		out.BlockingReasons = append(out.BlockingReasons, "promotion_source_release_unproven")
	}
	proposal, proposed, err := ProposeSources(from.Definition, in.Target, out.Artifacts, "promotion")
	out.Artifacts, out.Changes = proposal.Artifacts, proposal.Changes
	out.BlockingReasons = append(out.BlockingReasons, proposal.BlockingReasons...)
	out.Target.ProposedDigest = proposed.Digest
	return out, proposed, err
}

// ProposeSources changes only compatible source contracts, retaining every
// target configuration field. Scope and serving evidence are checked by callers.
func ProposeSources(origin api.EnvironmentDefinition, target environmentsync.DesiredState, artifactsInput []api.EnvironmentGitPromotionArtifact, prefix string) (api.PreviewEnvironmentGitPromotionResponse, environmentsync.DesiredState, error) {
	out := api.PreviewEnvironmentGitPromotionResponse{Artifacts: slices.Clone(artifactsInput), Changes: []api.EnvironmentGitPromotionChange{}, BlockingReasons: []string{}}
	if out.Artifacts == nil {
		out.Artifacts = []api.EnvironmentGitPromotionArtifact{}
	}
	artifacts := map[string]api.EnvironmentGitPromotionArtifact{}
	for i, artifact := range out.Artifacts {
		if artifact.ImageDigest != "" && !validImageIdentity(artifact.ImageDigest) {
			artifact.ImageDigest = ""
			out.Artifacts[i] = artifact
			out.BlockingReasons = append(out.BlockingReasons, artifact.Resource+": "+prefix+"_artifact_provenance_mismatch")
		}
		if hasBuildArtifact(artifact) && !validBuildArtifact(artifact) {
			artifact.BuildArtifactID, artifact.BuildArtifactHash, artifact.ArtifactDigest, artifact.BuildCommitSHA = "", "", "", ""
			out.Artifacts[i] = artifact
			out.BlockingReasons = append(out.BlockingReasons, artifact.Resource+": "+prefix+"_artifact_provenance_mismatch")
		}
		if _, exists := artifacts[artifact.Resource]; exists {
			out.BlockingReasons = append(out.BlockingReasons, prefix+"_artifact_ambiguous")
		}
		artifacts[artifact.Resource] = artifact
	}
	proposed, err := environmentsync.Compile(target.Definition)
	if err != nil || proposed.Digest != target.Digest {
		return out, environmentsync.DesiredState{}, fmt.Errorf("invalid target definition")
	}
	for _, name := range slices.Sorted(maps.Keys(origin.Workloads)) {
		resource := "workload/" + name
		origin, target := origin.Workloads[name], proposed.Definition.Workloads[name]
		if (origin.Schedule == nil) != (target.Schedule == nil) {
			out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_schedule_target_incompatible")
			continue
		}
		if origin.Source != nil && origin.Source.Kind != "image" {
			artifact, exists := artifacts[resource]
			if !exists || !validBuildArtifact(artifact) {
				out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_build_artifact_unproven")
				continue
			}
			if target.Source == nil || sourceRecipe(*origin.Source) != sourceRecipe(*target.Source) || origin.App == "" || origin.App != target.App {
				out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_build_artifact_target_incompatible")
				continue
			}
			after := *origin.Source
			after.BuildArtifactID, after.BuildArtifactHash = artifact.BuildArtifactID, artifact.BuildArtifactHash
			if after != *target.Source {
				out.Changes = append(out.Changes, api.EnvironmentGitPromotionChange{Resource: resource, Before: *target.Source, After: after})
				target.Source = &after
				proposed.Definition.Workloads[name] = target
			}
			continue
		}
		if origin.Source == nil {
			out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_source_artifact_reference_unsupported")
			continue
		}
		if !api.ValidDeploymentImage(origin.Source.Image) {
			out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_artifact_provenance_mismatch")
			continue
		}
		artifact, exists := artifacts[resource]
		_, digest, _ := strings.Cut(origin.Source.Image, "@")
		if !exists || artifact.DeploymentID == "" || artifact.Kind != "image" || artifact.ImageDigest != origin.Source.Image && artifact.ImageDigest != digest {
			out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_artifact_provenance_mismatch")
			continue
		}
		if target.Source == nil || target.Source.Kind != "image" || !api.ValidDeploymentImage(target.Source.Image) {
			out.BlockingReasons = append(out.BlockingReasons, resource+": "+prefix+"_target_image_contract_required")
			continue
		}
		if *origin.Source != *target.Source {
			out.Changes = append(out.Changes, api.EnvironmentGitPromotionChange{Resource: resource, Before: *target.Source, After: *origin.Source})
			copy := *origin.Source
			target.Source = &copy
			proposed.Definition.Workloads[name] = target
		}
	}
	if len(origin.Workloads) == 0 {
		out.BlockingReasons = append(out.BlockingReasons, prefix+"_source_workloads_missing")
	}
	proposed, err = environmentsync.Compile(proposed.Definition)
	out.Target.ProposedDigest = proposed.Digest
	slices.SortFunc(out.Artifacts, func(a, b api.EnvironmentGitPromotionArtifact) int { return strings.Compare(a.Resource, b.Resource) })
	return out, proposed, err
}

func validImageIdentity(value string) bool {
	if api.ValidDeploymentImage(value) {
		return true
	}
	digest, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func Finish(out api.PreviewEnvironmentGitPromotionResponse, targetPlan api.EnvironmentGitOpsPlan) (api.PreviewEnvironmentGitPromotionResponse, error) {
	out.PlanHash = ""
	out.TargetPlan = targetPlan
	out.BlockingReasons = append(out.BlockingReasons, targetPlan.BlockingReasons...)
	for _, change := range targetPlan.Changes {
		if change.Action == "overridden" {
			out.BlockingReasons = append(out.BlockingReasons, change.Resource+"/"+change.Path+": promotion_target_override_active")
		}
	}
	slices.Sort(out.BlockingReasons)
	out.BlockingReasons = slices.Compact(out.BlockingReasons)
	// Stable, separate namespace: this observation cannot be an adoption receipt.
	observedAt := out.ObservedAt
	out.ObservedAt = time.Time{}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(append([]byte("gregale.gitops.promotion-plan/v1\x00"), raw...))
	out.ObservedAt = observedAt
	out.PlanHash = hex.EncodeToString(digest[:])
	return out, nil
}

func provider(source api.EnvironmentGitSource) string {
	if source.Spec.Provider == "" {
		return "github"
	}
	return source.Spec.Provider
}

func sourceRecipe(source api.EnvironmentWorkloadSource) api.EnvironmentWorkloadSource {
	source.BuildArtifactID, source.BuildArtifactHash = "", ""
	return source
}

func validBuildCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func hasBuildArtifact(artifact api.EnvironmentGitPromotionArtifact) bool {
	return artifact.BuildArtifactID != "" || artifact.BuildArtifactHash != "" || artifact.ArtifactDigest != "" || artifact.BuildCommitSHA != ""
}
func validBuildArtifact(artifact api.EnvironmentGitPromotionArtifact) bool {
	return api.ValidEnvironmentBuildArtifactReference(artifact.BuildArtifactID, artifact.BuildArtifactHash) && len(artifact.ArtifactDigest) == 71 && strings.HasPrefix(artifact.ArtifactDigest, "sha256:") && validImageIdentity(artifact.ArtifactDigest) && artifact.BuildID != "" && validBuildCommit(artifact.BuildCommitSHA)
}

func HasBuildArtifact(a api.EnvironmentGitPromotionArtifact) bool   { return hasBuildArtifact(a) }
func ValidBuildArtifact(a api.EnvironmentGitPromotionArtifact) bool { return validBuildArtifact(a) }
func ValidImageIdentity(value string) bool                          { return validImageIdentity(value) }
func SourceRecipe(s api.EnvironmentWorkloadSource) api.EnvironmentWorkloadSource {
	return sourceRecipe(s)
}
