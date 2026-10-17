// Package proposal opens reviewed Git changes, without approving or deploying them.
// adr: 1550
package proposal

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"gopkg.in/yaml.v3"
)

var ErrNotFound = errors.New("proposal not found")

type Repository struct {
	ID   int64
	Path string
}

type File struct {
	Content    []byte
	Mode       string
	Blob       string
	Tree       string
	LastCommit string
}

type Review struct {
	Number                               int64
	URL, State, Branch, Base, Head, Body string
	RepositoryID, TargetRepositoryID     int64
}

// Provider never updates an existing branch, review or target ref.
type Provider interface {
	Repository(context.Context) (Repository, error)
	Head(context.Context, string) (string, error)
	File(context.Context, string, string) (File, error)
	Reviews(context.Context, string, string) ([]Review, error)
	Verify(context.Context, string, string, string, File) error
	Commit(context.Context, string, string, string, File, string) (string, error)
	Open(context.Context, string, string, string, string) (Review, error)
}

type Request struct {
	Plan             api.PreviewEnvironmentGitPromotionResponse
	Source           api.EnvironmentGitSource
	ExpectedPlanHash string
}

type Result struct {
	Status                      string `json:"status"`
	Provider                    string `json:"provider"`
	Repository                  string `json:"repository"`
	Branch                      string `json:"branch"`
	Base                        string `json:"base"`
	BaseCommit                  string `json:"base_commit"`
	PlanHash                    string `json:"plan_hash"`
	ManifestPath                string `json:"manifest_path"`
	Number                      int64  `json:"number,omitempty"`
	URL                         string `json:"url,omitempty"`
	Head                        string `json:"head_commit,omitempty"`
	ReleaseSetID                string `json:"release_set_id,omitempty"`
	RequiresTargetQualification bool   `json:"requires_target_qualification"`
}

// ValidateObservation binds a fresh planner response and source binding to the
// reviewed request. Observation timestamps may change; scope and intent may not.
func ValidateObservation(previous, fresh Request) error {
	if err := previous.validate(); err != nil {
		return err
	}
	if err := fresh.validate(); err != nil {
		return err
	}
	if previous.ExpectedPlanHash != fresh.ExpectedPlanHash || previous.Plan.Project != fresh.Plan.Project ||
		previous.Plan.FromEnvironment != fresh.Plan.FromEnvironment || previous.Plan.ToEnvironment != fresh.Plan.ToEnvironment ||
		previous.Source.Spec != fresh.Source.Spec || previous.Source.ID != fresh.Source.ID || previous.Source.Generation != fresh.Source.Generation || previous.Source.IntentVersion != fresh.Source.IntentVersion {
		return fmt.Errorf("promotion observation or target binding changed")
	}
	return nil
}

func (r Request) validate() error {
	p, s := r.Plan, r.Source
	provider := s.Spec.Provider
	if provider == "" {
		provider = "github"
	}
	if err := promotion.Validate(p); err != nil {
		return err
	}
	if err := s.Spec.Validate(); err != nil {
		return fmt.Errorf("invalid target source: %w", err)
	}
	if len(p.BlockingReasons) != 0 || len(p.Changes) == 0 || p.Target.DefinitionDigest == p.Target.ProposedDigest {
		return fmt.Errorf("promotion is blocked or has no source changes")
	}
	if !validHex(r.ExpectedPlanHash, 64) || p.PlanHash != r.ExpectedPlanHash || !validHex(p.Target.CommitSHA, 40) ||
		s.ID != p.Target.SourceID || s.Generation != p.Target.Generation || s.IntentVersion != p.Target.IntentVersion ||
		s.EnvironmentSlug != p.ToEnvironment || s.Detached || s.Suspended || s.SourceErrorCode != "" ||
		s.Spec.ManifestPath != p.Target.ManifestPath || s.Spec.RepositoryID != p.Origin.RepositoryID || provider != p.Origin.Provider ||
		!strings.HasPrefix(s.Spec.Ref, "refs/heads/") || !validRepository(s.Spec.Repository, provider) {
		return fmt.Errorf("reviewed promotion no longer matches the active target branch binding")
	}
	return nil
}

func validRepository(s, provider string) bool {
	parts := strings.Split(s, "/")
	if len(parts) < 2 || provider == "github" && len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
				return false
			}
		}
	}
	return true
}

func validHex(s string, n int) bool {
	_, err := hex.DecodeString(s)
	return len(s) == n && strings.ToLower(s) == s && err == nil
}

// Run previews by default. apply requires an explicit fresh observation callback;
// a proposal cannot turn the preview hash into approval or execution authority.
func Run(ctx context.Context, r Request, provider Provider, apply bool, recheck func(context.Context) error) (Result, error) {
	if err := r.validate(); err != nil {
		return Result{}, err
	}
	title, body := describe(r.Plan)
	return run(ctx, preparedRequest{source: r.Source, target: r.Plan.Target, kind: "promotion", provider: r.Plan.Origin.Provider,
		hash: r.Plan.PlanHash, branchPrefix: "gregale-promote-", title: title, body: body,
		export: func(raw []byte) ([]byte, error) { return exportManifest(r.Plan, raw) }}, provider, apply, recheck)
}

// Only operation-specific validators construct this private write intent.
// In particular, rollback is never recast or rehashed as a promotion plan.
type preparedRequest struct {
	source                                      api.EnvironmentGitSource
	target                                      api.EnvironmentGitPromotionTarget
	kind, provider, hash, branchPrefix, release string
	title, body                                 string
	export                                      func([]byte) ([]byte, error)
}

func run(ctx context.Context, request preparedRequest, provider Provider, apply bool, recheck func(context.Context) error) (Result, error) {
	source := request.source
	out := Result{Status: "preview", Provider: request.provider, Repository: source.Spec.Repository,
		Branch: request.branchPrefix + request.hash, Base: strings.TrimPrefix(source.Spec.Ref, "refs/heads/"),
		BaseCommit: request.target.CommitSHA, PlanHash: request.hash, ManifestPath: request.target.ManifestPath,
		ReleaseSetID: request.release, RequiresTargetQualification: true}
	if provider == nil {
		return out, fmt.Errorf("provider is unavailable")
	}
	repository, err := provider.Repository(ctx)
	if err != nil {
		return out, err
	}
	if repository.ID != source.Spec.RepositoryID || !strings.EqualFold(repository.Path, out.Repository) {
		return out, fmt.Errorf("provider repository does not match the verified binding")
	}
	checkHead := func() error {
		head, err := provider.Head(ctx, out.Base)
		if err != nil {
			return err
		}
		if head != out.BaseCommit {
			return fmt.Errorf("target branch moved; request and review a fresh %s plan", request.kind)
		}
		return nil
	}
	if err := checkHead(); err != nil {
		return out, err
	}
	base, err := provider.File(ctx, out.BaseCommit, out.ManifestPath)
	if err != nil {
		return out, err
	}
	if base.Mode != "100644" {
		return out, fmt.Errorf("target manifest must be a regular non-executable file")
	}
	content, err := request.export(base.Content)
	if err != nil {
		return out, err
	}
	file := base
	file.Content = content
	title, body := request.title, request.body
	// Read all states so a closed proposal is never silently reopened or duplicated.
	reviews, err := provider.Reviews(ctx, out.Branch, out.Base)
	if err != nil {
		return out, err
	}
	if len(reviews) > 1 {
		return out, fmt.Errorf("ambiguous existing %s proposals", request.kind)
	}
	head, err := provider.Head(ctx, out.Branch)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	if errors.Is(err, ErrNotFound) {
		head = ""
	}
	if head != "" {
		if err := provider.Verify(ctx, head, out.BaseCommit, out.ManifestPath, file); err != nil {
			return out, err
		}
	}
	checkProposalHead := func() error {
		current, err := provider.Head(ctx, out.Branch)
		if err != nil || current != head {
			return fmt.Errorf("proposal branch changed; inspect it before retrying")
		}
		return nil
	}
	if len(reviews) == 1 {
		review := reviews[0]
		if err := verifyReview(review, out, repository.ID, head, body); err != nil {
			return out, err
		}
		if err := checkProposalHead(); err != nil {
			return out, err
		}
		if err := checkHead(); err != nil {
			return out, err
		}
		out.Status, out.Number, out.URL, out.Head = "existing", review.Number, review.URL, head
		return out, nil
	}
	if !apply {
		if err := checkHead(); err != nil {
			return out, err
		}
		return out, nil
	}
	if recheck == nil {
		return out, fmt.Errorf("a fresh %s observation is required before writing", request.kind)
	}
	if err := recheck(ctx); err != nil {
		return out, err
	}
	if err := checkHead(); err != nil {
		return out, err
	}
	if head == "" {
		head, err = provider.Commit(ctx, out.BaseCommit, out.Branch, out.ManifestPath, file, title)
		if err != nil {
			return out, fmt.Errorf("could not create proposal branch; retry the same command to recover: %w", err)
		}
	}
	if err := provider.Verify(ctx, head, out.BaseCommit, out.ManifestPath, file); err != nil {
		return out, err
	}
	if err := recheck(ctx); err != nil {
		return out, err
	}
	if err := checkHead(); err != nil {
		return out, err
	}
	if err := checkProposalHead(); err != nil {
		return out, err
	}
	// Never repeat an uncertain provider write automatically. A subsequent explicit
	// retry discovers the deterministic branch and exact matching review.
	review, err := provider.Open(ctx, out.Branch, out.Base, title, body)
	if err != nil {
		return out, fmt.Errorf("could not confirm proposal creation; retry the same command to recover: %w", err)
	}
	if err := verifyReview(review, out, repository.ID, head, body); err != nil {
		return out, err
	}
	if err := checkHead(); err != nil {
		return out, err
	}
	out.Status, out.Number, out.URL, out.Head = "created", review.Number, review.URL, head
	return out, nil
}

func verifyReview(r Review, out Result, repositoryID int64, head, body string) error {
	u, err := url.Parse(r.URL)
	host, prefix := "github.com", "/"+out.Repository+"/pull/"
	if out.Provider == "gitlab" {
		host, prefix = "gitlab.com", "/"+out.Repository+"/-/merge_requests/"
	}
	if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Path != fmt.Sprintf("%s%d", prefix, r.Number) || r.Number <= 0 ||
		r.State != "open" || r.Branch != out.Branch || r.Base != out.Base || head == "" || r.Head != head || strings.TrimRight(r.Body, "\r\n") != strings.TrimRight(body, "\r\n") ||
		r.RepositoryID != repositoryID || r.TargetRepositoryID != repositoryID {
		return fmt.Errorf("existing or returned proposal is closed, changed or does not match the reviewed plan")
	}
	return nil
}

func exportManifest(plan api.PreviewEnvironmentGitPromotionResponse, raw []byte) ([]byte, error) {
	return exportSources(raw, plan.Changes, plan.Target.ProposedDigest, func(definition api.EnvironmentDefinition) ([]byte, error) {
		return promotion.ExportManifest(plan, definition)
	})
}

func exportSources(raw []byte, changes []api.EnvironmentGitPromotionChange, digest string, export func(api.EnvironmentDefinition) ([]byte, error)) ([]byte, error) {
	definition, err := gregalemanifest.ParseEnvironment(raw)
	if err != nil {
		return nil, fmt.Errorf("provider target manifest is invalid")
	}
	canonical, err := export(definition)
	if err != nil {
		return nil, err
	}
	if json.Valid(raw) {
		if len(canonical) > api.EnvironmentGitOpsMaxDefinitionBytes {
			return nil, fmt.Errorf("proposed manifest exceeds the definition size bound")
		}
		return canonical, nil
	}
	// Retain YAML comments and all target configuration nodes. The existing
	// exporter has already proved the complete base and proposed digests.
	var root yaml.Node
	if yaml.Unmarshal(raw, &root) != nil || len(root.Content) != 1 {
		return nil, fmt.Errorf("invalid target YAML")
	}
	workloads := mappingValue(root.Content[0], "workloads")
	for _, change := range changes {
		workload := mappingValue(workloads, strings.TrimPrefix(change.Resource, "workload/"))
		source := mappingValue(workload, "source")
		if source == nil {
			return nil, fmt.Errorf("target YAML source is missing")
		}
		encoded, err := json.Marshal(change.After)
		if err != nil {
			return nil, err
		}
		var replacement yaml.Node
		if yaml.Unmarshal(encoded, &replacement) != nil {
			return nil, fmt.Errorf("invalid proposed source")
		}
		next := *replacement.Content[0]
		next.Style, next.HeadComment, next.LineComment, next.FootComment = source.Style, source.HeadComment, source.LineComment, source.FootComment
		*source = next
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return nil, fmt.Errorf("could not encode proposed YAML")
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	checked, err := gregalemanifest.ParseEnvironment(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("proposed YAML exceeds the manifest contract")
	}
	compiled, err := environmentsync.Compile(checked)
	if err != nil || compiled.Digest != digest {
		return nil, fmt.Errorf("proposed YAML digest mismatch")
	}
	return out.Bytes(), nil
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func describe(p api.PreviewEnvironmentGitPromotionResponse) (string, string) {
	title := fmt.Sprintf("Promote %s from %s to %s", p.Project, p.FromEnvironment, p.ToEnvironment)
	var body strings.Builder
	fmt.Fprintf(&body, "<!-- gregale-promotion:%s -->\n\nPromote the proven serving release `%s` from **%s** to **%s**.\n\nPlan: `%s`\nTarget base: `%s`\nSource manifest commit: `%s`\n\n", p.PlanHash, safe(p.Origin.ReleaseSetID), p.FromEnvironment, p.ToEnvironment, p.PlanHash, p.Target.CommitSHA, safe(p.Origin.CommitSHA))
	for _, c := range p.Changes {
		fmt.Fprintf(&body, "- `%s`: `%s` → `%s`\n", safe(c.Resource), safe(sourceLabel(c.Before)), safe(sourceLabel(c.After)))
	}
	for _, a := range p.Artifacts {
		if a.BuildArtifactID != "" {
			fmt.Fprintf(&body, "- `%s`: signed layer `%s`, build `%s`, code commit `%s`\n", safe(a.Resource), safe(a.ArtifactDigest), safe(a.BuildID), safe(a.BuildCommitSHA))
		}
	}
	body.WriteString("\nOnly workload source contracts change. Target configuration, secret references, routes, policies, schedules and bindings are preserved.\n\nNormal Git review and approval, current artifact trust, and fresh target qualification, activation and serving evidence are still required. This proposal grants no deployment authority. Exclusive native VM/leak acceptance remains pending; production qualification and enforcement gates are unchanged.\n")
	return title, body.String()
}

func sourceLabel(s api.EnvironmentWorkloadSource) string {
	if s.Kind == "image" {
		return s.Image
	}
	return s.Kind + " artifact " + s.BuildArtifactID + " input sha256:" + s.BuildArtifactHash
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune("`<>[]\\", r) {
			return ' '
		}
		return r
	}, s)
}
