// Package rollback prepares code-only recovery proposals from retained serving evidence.
package rollback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/diagnostics"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type Input struct {
	Source              api.EnvironmentGitSource
	Revision            api.EnvironmentDesiredRevision
	Origin              api.EnvironmentGitPromotionOrigin
	ServedAt            *time.Time
	Proven              bool
	CurrentReleaseSetID string
	TargetCommit        string
	Target              environmentsync.DesiredState
	Artifacts           []api.EnvironmentGitPromotionArtifact
	BlockingReasons     []string
	Now                 time.Time
}

func Prepare(in Input) (api.PreviewEnvironmentGitRollbackResponse, environmentsync.DesiredState, error) {
	var out api.PreviewEnvironmentGitRollbackResponse
	if in.Source.ID == "" || in.Source.AccountID == "" || in.Source.ProjectID == "" || in.Source.EnvironmentID == "" || in.Origin.SourceID != in.Source.ID || in.Origin.ReleaseSetID == "" || in.Revision.SourceID != in.Source.ID || in.Origin.RevisionID != in.Revision.ID {
		return out, environmentsync.DesiredState{}, fmt.Errorf("rollback scope mismatch")
	}
	var definition api.EnvironmentDefinition
	if json.Unmarshal(in.Revision.Definition, &definition) != nil {
		return out, environmentsync.DesiredState{}, fmt.Errorf("invalid retained definition")
	}
	origin, err := environmentsync.Compile(definition)
	if err != nil || origin.Digest != in.Revision.Digest || in.Origin.DefinitionDigest != origin.Digest || in.Origin.CommitSHA != in.Revision.CommitSHA || definition.Project != in.Target.Definition.Project || definition.Environment != in.Source.EnvironmentSlug || in.Target.Definition.Environment != in.Source.EnvironmentSlug {
		return out, environmentsync.DesiredState{}, fmt.Errorf("rollback definition identity mismatch")
	}
	out = api.PreviewEnvironmentGitRollbackResponse{Project: definition.Project, Environment: in.Source.EnvironmentSlug, ObservedAt: in.Now.UTC(), ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true, CurrentReleaseSetID: in.CurrentReleaseSetID, Origin: in.Origin, OriginServedAt: in.ServedAt,
		Target: api.EnvironmentGitPromotionTarget{SourceID: in.Source.ID, Generation: in.Source.Generation, IntentVersion: in.Source.IntentVersion, CommitSHA: in.TargetCommit, DefinitionDigest: in.Target.Digest, ManifestPath: in.Source.Spec.ManifestPath}, BlockingReasons: slices.Clone(in.BlockingReasons)}
	if !in.Proven || in.ServedAt == nil || in.ServedAt.IsZero() || in.Origin.GraphID == "" {
		out.BlockingReasons = append(out.BlockingReasons, "rollback_release_unproven")
	}
	assessment := diagnostics.Assess(api.EnvironmentGitOpsStatusResponse{Source: in.Source}, in.Now)
	if in.Source.Detached || in.Source.Suspended {
		out.BlockingReasons = append(out.BlockingReasons, "rollback_source_inactive")
	}
	if assessment.SourceFreshness != "fresh" || in.Source.SourceErrorCode != "" {
		out.BlockingReasons = append(out.BlockingReasons, "rollback_source_unverified")
	}
	proposal, proposed, err := promotion.ProposeSources(origin.Definition, in.Target, in.Artifacts, "rollback")
	out.Artifacts, out.Changes = proposal.Artifacts, proposal.Changes
	out.BlockingReasons = append(out.BlockingReasons, proposal.BlockingReasons...)
	out.Target.ProposedDigest = proposed.Digest
	return out, proposed, err
}

func Finish(out api.PreviewEnvironmentGitRollbackResponse, plan api.EnvironmentGitOpsPlan) (api.PreviewEnvironmentGitRollbackResponse, error) {
	out.PlanHash = ""
	out.TargetPlan = plan
	out.BlockingReasons = append(out.BlockingReasons, plan.BlockingReasons...)
	for _, change := range plan.Changes {
		if change.Action == "overridden" {
			out.BlockingReasons = append(out.BlockingReasons, change.Resource+"/"+change.Path+": rollback_target_override_active")
		}
	}
	slices.Sort(out.BlockingReasons)
	out.BlockingReasons = slices.Compact(out.BlockingReasons)
	observed := out.ObservedAt
	out.ObservedAt = time.Time{}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(append([]byte("gregale.gitops.rollback-plan/v1\x00"), raw...))
	out.ObservedAt = observed
	out.PlanHash = hex.EncodeToString(digest[:])
	return out, nil
}
