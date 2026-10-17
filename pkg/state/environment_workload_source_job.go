package state

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Source Jobs reuse the qualified deployment layer, not an OCI pull or a new
// build per occurrence. These disjoint internal identities never name a digest.
const environmentGitOpsSourceJobPrefix = "gregale-source://"
const environmentGitOpsJobDeploymentPrefix = "gregale-deployment:"

func isEnvironmentGitOpsSourceJob(ref string) bool {
	return strings.HasPrefix(ref, environmentGitOpsSourceJobPrefix)
}

func environmentGitOpsJobSourceRef(intent EnvironmentWorkloadIntent) (string, bool) {
	if intent.Source == nil {
		return "", false
	}
	if intent.Source.Kind == "image" {
		_, ok := immutableEnvironmentJobImageDigest(intent.Source.Image)
		return intent.Source.Image, ok
	}
	if (intent.Source.Kind != "source" && intent.Source.Kind != "dockerfile") ||
		!canonicalStateUUID(intent.EnvironmentID) || !canonicalStateUUID(intent.AppID) {
		return "", false
	}
	prefix := environmentGitOpsSourceJobPrefix + intent.EnvironmentID + "/" + intent.AppID + "/"
	if intent.Source.BuildArtifactID != "" || intent.Source.BuildArtifactHash != "" {
		if intent.SourceRevision != "" || !api.ValidEnvironmentBuildArtifactReference(intent.Source.BuildArtifactID, intent.Source.BuildArtifactHash) {
			return "", false
		}
		return prefix + "artifact/" + intent.Source.BuildArtifactID + "/" + intent.Source.BuildArtifactHash, true
	}
	return prefix + intent.SourceRevision, environmentCommitRE.MatchString(intent.SourceRevision)
}

// EnvironmentGitOpsJobCommand keeps production argv separate from the
// qualification smoke command. Source schedules require an explicit command.
func EnvironmentGitOpsJobCommand(intent EnvironmentWorkloadIntent) ([]string, bool) {
	var command []string
	raw := intent.Runtime["entrypoint"]
	if len(raw) != 0 && string(raw) != "null" && json.Unmarshal(raw, &command) != nil {
		return nil, false
	}
	if len(command) == 0 {
		return []string{}, intent.Source != nil && intent.Source.Kind == "image"
	}
	return command, (api.EnvironmentJobSmoke{Command: command, TimeoutSeconds: 1}).Validate() == nil
}

// EnvironmentGitOpsJobSourceDeployment decodes only an internal source Job
// artifact identity. Callers must also validate the deployment and its release.
func EnvironmentGitOpsJobSourceDeployment(job Job) (string, bool) {
	id, ok := strings.CutPrefix(job.ImageResolvedDigest, environmentGitOpsJobDeploymentPrefix)
	return id, ok && isEnvironmentGitOpsSourceJob(job.ImageRef) && canonicalStateUUID(id)
}

// EnvironmentGitOpsJobSourceMatches checks source identity without granting
// execution authority. Source Jobs additionally require the exact qualified
// deployment in the graph and active release before the guest can start.
func EnvironmentGitOpsJobSourceMatches(intent EnvironmentWorkloadIntent, job Job) bool {
	ref, ok := environmentGitOpsJobSourceRef(intent)
	command, commandOK := EnvironmentGitOpsJobCommand(intent)
	if !ok || !commandOK || !slices.Equal(command, job.Command) || ref != job.ImageRef {
		return false
	}
	if intent.Source.Kind == "image" {
		digest, ok := immutableEnvironmentJobImageDigest(ref)
		return ok && digest == job.ImageResolvedDigest
	}
	_, ok = EnvironmentGitOpsJobSourceDeployment(job)
	return ok
}

// EnvironmentGitOpsSourceJobArtifactMatches revalidates the frozen reviewed
// source and exact layer selected at activation. The layer key remains owned
// and retained by the deployment; Jobs must never publish or collect it.
func EnvironmentGitOpsSourceJobArtifactMatches(intent EnvironmentWorkloadIntent, job Job, dep Deployment) bool {
	id, ok := EnvironmentGitOpsJobSourceDeployment(job)
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil {
		return false
	}
	command, commandOK := EnvironmentGitOpsJobCommand(EnvironmentWorkloadIntent{Source: frozen.Source, Runtime: frozen.Runtime})
	codeMatches := frozen.SourceArchive != nil && dep.CommitSHA == intent.SourceRevision
	if frozen.BuiltArtifact != nil {
		// ScopedWorkloadRuntime validates the exact immutable borrowed receipt.
		// Its code commit differs from the reviewed target manifest commit.
		codeMatches = intent.SourceRevision == "" && builtArtifactMatchesDeployment(dep, *frozen)
	}
	return commandOK && slices.Equal(command, job.Command) && ok && EnvironmentGitOpsJobSourceMatches(intent, job) && id == dep.ID && dep.AppID == intent.AppID &&
		codeMatches && dep.RootfsKey != "" && dep.RootfsBytes > 0 && job.ImageStorageKey == dep.RootfsKey &&
		frozen.WorkloadClass == WorkloadClassJob && frozen.Schedule != nil && frozen.EnvironmentID == intent.EnvironmentID &&
		frozen.AppID == intent.AppID && reflect.DeepEqual(frozen.Source, intent.Source)
}

func environmentGraphSourceJobArtifactMatches(graph EnvironmentWorkloadGraph, member EnvironmentWorkloadGraphMember,
	intent EnvironmentWorkloadIntent, job Job, dep Deployment) bool {
	if !EnvironmentGitOpsSourceJobArtifactMatches(intent, job, dep) ||
		(member.CandidateDeploymentID != dep.ID && (member.CandidateDeploymentID != "" || !slices.Contains(member.RetainedDeployments, dep.ID))) {
		return false
	}
	frozen, _ := dep.ScopedWorkloadRuntime()
	return frozen.SourceID == graph.SourceID && frozen.EnvironmentID == graph.EnvironmentID &&
		(member.CandidateDeploymentID == "" || frozen.RevisionID == graph.RevisionID && frozen.Generation == graph.Generation &&
			frozen.PlanHash == graph.PlanHash)
}

func bindEnvironmentGitOpsSourceJob(graph EnvironmentWorkloadGraph, member EnvironmentWorkloadGraphMember,
	intent EnvironmentWorkloadIntent, job Job, dep Deployment) (Job, bool) {
	if job.Status != "paused" || job.ID != intent.JobID || job.AccountID != intent.AccountID || dep.Status != DeploySnapshotting && dep.Status != DeployLive {
		return job, false
	}
	bound := job
	bound.ImageResolvedDigest, bound.ImageStorageKey = environmentGitOpsJobDeploymentPrefix+dep.ID, dep.RootfsKey
	bound.ImageMaterializationStatus, bound.ImageMaterializationError = "ready", ""
	bound.ImageMaterializationNextAttemptAt = nil
	if !environmentGraphSourceJobArtifactMatches(graph, member, intent, bound, dep) ||
		!environmentGitOpsScheduledJobMatches(graph, member, intent, bound, "paused") {
		return job, false
	}
	if job.ImageResolvedDigest != bound.ImageResolvedDigest || job.ImageStorageKey != bound.ImageStorageKey {
		bound.ScheduleRevision++
		bound.LastScheduledAt = nil
	}
	now := time.Now().UTC()
	bound.ImageMaterializedAt, bound.UpdatedAt = &now, now
	return bound, true
}
