package proposal

// adr: 1551
import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
)

// RollbackRequest keeps the explicitly selected release and rollback observation
// separate from promotion. Historical proof never grants current readiness.
type RollbackRequest struct {
	Plan             api.PreviewEnvironmentGitRollbackResponse
	Source           api.EnvironmentGitSource
	ReleaseSetID     string
	ExpectedPlanHash string
}

func (r RollbackRequest) validate() error {
	p, s := r.Plan, r.Source
	if err := (api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: r.ReleaseSetID, TargetCommitSHA: p.Target.CommitSHA, ExpectedPlanHash: r.ExpectedPlanHash}).Validate(); err != nil {
		return err
	}
	if err := rollback.Validate(p); err != nil {
		return err
	}
	if err := s.Spec.Validate(); err != nil {
		return fmt.Errorf("invalid target source: %w", err)
	}
	if len(p.BlockingReasons) != 0 || len(p.Changes) == 0 || p.Target.DefinitionDigest == p.Target.ProposedDigest {
		return fmt.Errorf("rollback is blocked or has no source changes")
	}
	provider := s.Spec.Provider
	if provider == "" {
		provider = "github"
	}
	if !validHex(r.ExpectedPlanHash, 64) || p.PlanHash != r.ExpectedPlanHash || p.Origin.ReleaseSetID != r.ReleaseSetID ||
		p.OriginServedAt == nil || p.OriginServedAt.IsZero() || p.Origin.GraphID == "" || !validHex(p.Origin.CommitSHA, 40) ||
		s.ID != p.Target.SourceID || s.Generation != p.Target.Generation || s.IntentVersion != p.Target.IntentVersion || s.EnvironmentSlug != p.Environment ||
		s.Detached || s.Suspended || s.SourceErrorCode != "" || s.Spec.ManifestPath != p.Target.ManifestPath ||
		s.Spec.RepositoryID != p.Origin.RepositoryID || provider != p.Origin.Provider || !strings.HasPrefix(s.Spec.Ref, "refs/heads/") || !validRepository(s.Spec.Repository, provider) {
		return fmt.Errorf("reviewed rollback no longer matches the selected served release and active target branch binding")
	}
	return nil
}

// ValidateRollbackObservation requires a fresh planner result with the same
// selected historical release, current release, target intent and bound source.
func ValidateRollbackObservation(previous, fresh RollbackRequest) error {
	if err := previous.validate(); err != nil {
		return err
	}
	if err := fresh.validate(); err != nil {
		return err
	}
	if previous.ExpectedPlanHash != fresh.ExpectedPlanHash || previous.ReleaseSetID != fresh.ReleaseSetID ||
		previous.Plan.Project != fresh.Plan.Project || previous.Plan.Environment != fresh.Plan.Environment ||
		previous.Plan.CurrentReleaseSetID != fresh.Plan.CurrentReleaseSetID || previous.Source.Spec != fresh.Source.Spec ||
		previous.Source.ID != fresh.Source.ID || previous.Source.Generation != fresh.Source.Generation || previous.Source.IntentVersion != fresh.Source.IntentVersion {
		return fmt.Errorf("rollback observation, selected release or target binding changed")
	}
	return nil
}

func RunRollback(ctx context.Context, r RollbackRequest, provider Provider, apply bool, recheck func(context.Context) error) (Result, error) {
	if err := r.validate(); err != nil {
		return Result{}, err
	}
	title, body := describeRollback(r.Plan)
	return run(ctx, preparedRequest{source: r.Source, target: r.Plan.Target, kind: "rollback", provider: r.Plan.Origin.Provider,
		hash: r.Plan.PlanHash, branchPrefix: "gregale-rollback-", release: r.ReleaseSetID, title: title, body: body,
		export: func(raw []byte) ([]byte, error) {
			return exportSources(raw, r.Plan.Changes, r.Plan.Target.ProposedDigest, func(definition api.EnvironmentDefinition) ([]byte, error) {
				return rollback.ExportManifest(r.Plan, definition)
			})
		}}, provider, apply, recheck)
}

func describeRollback(p api.PreviewEnvironmentGitRollbackResponse) (string, string) {
	title := fmt.Sprintf("Recover %s / %s to release %s", p.Project, p.Environment, p.Origin.ReleaseSetID)
	var body strings.Builder
	fmt.Fprintf(&body, "<!-- gregale-rollback:%s -->\n\nRecover **%s / %s** to the explicitly selected previously served release `%s`.\n\nPlan: `%s`\nTarget base: `%s`\nCurrent release: `%s`\nHistorical manifest commit: `%s`\nHistorical graph: `%s`\nHistorical serving time: `%s`\n\n", p.PlanHash, p.Project, p.Environment, safe(p.Origin.ReleaseSetID), p.PlanHash, p.Target.CommitSHA, safe(p.CurrentReleaseSetID), safe(p.Origin.CommitSHA), safe(p.Origin.GraphID), p.OriginServedAt.UTC().Format(time.RFC3339Nano))
	for _, c := range p.Changes {
		fmt.Fprintf(&body, "- `%s`: `%s` → `%s`\n", safe(c.Resource), safe(sourceLabel(c.Before)), safe(sourceLabel(c.After)))
	}
	for _, a := range p.Artifacts {
		if a.BuildArtifactID != "" {
			fmt.Fprintf(&body, "- `%s`: retained signed layer `%s`, build `%s`, code commit `%s`\n", safe(a.Resource), safe(a.ArtifactDigest), safe(a.BuildID), safe(a.BuildCommitSHA))
		}
	}
	body.WriteString("\nOnly workload source contracts change. Current target configuration, secret references, routes, policies, schedules and bindings are preserved.\n\nThis is code recovery: it does not restore data or undo database migrations. Completed Jobs are never replayed. Scheduled Jobs require fresh qualification and a successful post-activation occurrence for future runs.\n\nNormal Git review and approval, current artifact trust, and fresh target qualification, activation and serving evidence are still required. Historical serving proof is selection evidence, not current readiness. Creating this proposal grants no deployment authority, artifact pin or retention extension. Exclusive native VM/leak acceptance remains pending; production qualification and enforcement gates are unchanged.\n")
	return title, body.String()
}
