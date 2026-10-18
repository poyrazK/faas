package mcpmanagement

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type planSourceDeployArgs struct {
	RolloutMode string `json:"rollout_mode,omitempty" jsonschema:"For service or worker source activation specify service or worker; omit for request-mode staging"`
	App         string `json:"app" jsonschema:"Existing Gregale app slug"`
	Repo        string `json:"repo" jsonschema:"Connected GitHub owner/repository; no URLs or credentials"`
	Ref         string `json:"ref" jsonschema:"GitHub branch or exact lowercase 40-character commit SHA. Branches resolve once during planning"`
	Reason      string `json:"reason" jsonschema:"Reviewable release reason; at most 280 characters"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
}

var sourceRepository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9._-]*$`)
var sourceCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

func (m *management) planSourceDeploy(ctx context.Context, args planSourceDeployArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if len(args.Repo) > 200 || !sourceRepository.MatchString(args.Repo) || len(args.Ref) == 0 || len(args.Ref) > 200 || strings.ContainsAny(args.Ref, " \t\r\n:@\\?#") || strings.HasPrefix(args.Ref, "refs/tags/") {
		return nil, invalid("invalid_argument", "source requires a connected GitHub owner/repository and a branch or full lowercase commit SHA; use the commit SHA for tags")
	}
	action := "deploy"
	if args.RolloutMode != "" {
		if args.RolloutMode != api.ExecutionModeService && args.RolloutMode != api.ExecutionModeWorker {
			return nil, invalid("invalid_argument", "rollout_mode must be service or worker")
		}
		action = "workload_rollout"
	}
	p, err := m.captureScopedPlan(ctx, args.App, args.Project, args.Environment, args.Reason, action)
	if err != nil {
		return nil, err
	}
	if args.RolloutMode != "" && p.RolloutMode != args.RolloutMode {
		return nil, invalid("unsupported_release", "reviewed workload mode differs from requested source rollout")
	}
	source, err := m.client.ReviewSourceRef(ctx, args.App, api.SourceReviewRequest{Repo: args.Repo, Ref: args.Ref, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	if source.AppID != p.AppID || !strings.EqualFold(source.Repo, args.Repo) || !sourceCommit.MatchString(source.CommitSHA) || source.InstallationID <= 0 || source.RepositoryID <= 0 || len(source.SourceRoot) > 1024 || !reflect.DeepEqual(source.ReleaseReview, *p.review) || (sourceCommit.MatchString(args.Ref) && source.CommitSHA != args.Ref) {
		return nil, invalid("plan_stale", "source or target identity changed during review; inspect and plan again")
	}
	p.Source, p.RequestedRef = &source, args.Ref
	p.ExpiresAt = time.Now().Add(releasePlanTTL + time.Duration(api.BuildTimeoutSeconds)*time.Second)
	p.TrafficEffect = "Build the exact reviewed Git commit through Gregale and stage it at 0% traffic. Preserve target workload configuration; do not import repository trigger, workflow, operation or release-command declarations. Promotion is separate and retains protected-environment approval gates."
	if action == "workload_rollout" {
		p.TrafficEffect = "Build the exact reviewed Git commit preserving target configuration and declarations. After the artifact becomes live, services hand off through native readiness and drain gates; workers activate as the newest generation and replace consumers. No separate traffic promotion; external queue effects are not reversed."
	}
	return m.savePlan(p)
}

func (m *management) stageSourcePlan(ctx context.Context, p *releasePlan) (api.DeploymentResponse, error) {
	zero := 0
	environment := ""
	if p.EnvironmentID != "" {
		environment = p.Scope
	}
	request := api.SourceRefDeployRequest{Repo: p.Source.Repo, Ref: p.Source.CommitSHA, Format: "tarball", NoTriggers: true, Environment: environment, Reason: p.Reason, TrafficPercent: &zero, ExpectedSource: p.Source}
	key := "mcp-deploy-" + p.ID
	if p.Action == "workload_rollout" {
		request.TrafficPercent = nil
		request.NativeRollout = p.RolloutMode
		key = "mcp-workload-" + p.ID
	}
	return m.client.DeployFromSourceRef(api.ContextWithIdempotencyKey(ctx, key), p.App, request)
}
