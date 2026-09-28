package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type projectEnvironmentPreflightClient interface {
	projectEnvironmentQualificationClient
	GetProjectEnvironmentPromotionPreviewWithConfig(context.Context, string, string, string, bool) (api.ProjectEnvironmentPromotionPreviewResponse, error)
}

type projectEnvironmentPreflightQualification struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	ReleaseSetID string    `json:"release_set_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type projectEnvironmentPreflightPreview struct {
	CanPromote            bool `json:"can_promote"`
	QualificationRequired bool `json:"qualification_required"`
	ApprovalRequired      bool `json:"approval_required"`
	SyncConfig            bool `json:"sync_config,omitempty"`
}

// projectEnvironmentPreflightResponse deliberately exposes only the summary
// needed by CI. In particular, it omits qualification fingerprints and the
// promotion token returned by the underlying API preview.
type projectEnvironmentPreflightResponse struct {
	ProjectSlug      string                                   `json:"project_slug"`
	FromEnvironment  string                                   `json:"from_environment"`
	ToEnvironment    string                                   `json:"to_environment"`
	Status           string                                   `json:"status"`
	Qualification    projectEnvironmentPreflightQualification `json:"qualification"`
	PromotionPreview projectEnvironmentPreflightPreview       `json:"promotion_preview"`
	BlockingReasons  []string                                 `json:"blocking_reasons,omitempty"`
}

func cmdProjectsEnvironmentPreflight(args []string) int {
	flags, positional := splitArgsForFlags(args, "sync-config")
	fs := newFlagSet("projects-environments-preflight", flag.ContinueOnError)
	from := fs.String("from", "", "source environment to qualify and promote")
	to := fs.String("to", "", "target environment to check")
	profile := fs.String("profile", "", "YAML probe profile for every workload in the active source release set")
	syncConfig := fs.Bool("sync-config", false, "include non-secret source config in the promotion preview")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || !api.ValidProjectSlug(positional[0]) ||
		!api.ValidProjectEnvironmentSlug(strings.TrimSpace(*from)) || !api.ValidProjectEnvironmentSlug(strings.TrimSpace(*to)) ||
		strings.TrimSpace(*from) == strings.TrimSpace(*to) || strings.TrimSpace(*profile) == "" {
		PrintUsage(osStderr, "usage: gregale projects environments preflight <project-slug> --from <environment> --to <environment> --profile <FILE> [--sync-config]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := runProjectEnvironmentPreflight(context.Background(), client, positional[0], strings.TrimSpace(*from), strings.TrimSpace(*to), strings.TrimSpace(*profile), *syncConfig, nil)
	if err != nil {
		return printErr("Environment promotion preflight failed", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
		return projectEnvironmentPreflightExitCode(result)
	}
	state := strings.ToUpper(result.Status)
	_, _ = fmt.Fprintf(osStdout, "Promotion preflight %s %s -> %s: %s\n",
		result.ProjectSlug, result.FromEnvironment, result.ToEnvironment, state)
	_, _ = fmt.Fprintf(osStdout, "Qualification %s: %s (release set %s; expires %s)\n",
		result.Qualification.ID, result.Qualification.Status, result.Qualification.ReleaseSetID,
		result.Qualification.ExpiresAt.UTC().Format(time.RFC3339))
	previewStatus := "blocked"
	if result.PromotionPreview.CanPromote {
		previewStatus = "promotable"
	}
	_, _ = fmt.Fprintf(osStdout, "Promotion preview: %s", previewStatus)
	if result.PromotionPreview.SyncConfig {
		_, _ = fmt.Fprint(osStdout, "; non-secret config sync included")
	}
	if result.PromotionPreview.ApprovalRequired {
		_, _ = fmt.Fprint(osStdout, "; approval required")
	}
	_, _ = fmt.Fprintln(osStdout)
	for _, reason := range result.BlockingReasons {
		_, _ = fmt.Fprintf(osStdout, "  - %s\n", reason)
	}
	return projectEnvironmentPreflightExitCode(result)
}

func projectEnvironmentPreflightExitCode(result projectEnvironmentPreflightResponse) int {
	if result.Status != "ready" {
		return 1
	}
	return 0
}

func runProjectEnvironmentPreflight(ctx context.Context, client projectEnvironmentPreflightClient, projectSlug, fromEnvironment, toEnvironment, profilePath string, syncConfig bool, probeClient *http.Client) (projectEnvironmentPreflightResponse, error) {
	qualificationProfile, err := readProjectEnvironmentQualificationProfile(profilePath)
	if err != nil {
		return projectEnvironmentPreflightResponse{}, err
	}
	qualification, err := qualifyProjectEnvironmentWithProfileAndHTTPClient(ctx, client, projectSlug, fromEnvironment, qualificationProfile, probeClient)
	if err != nil {
		return projectEnvironmentPreflightResponse{}, err
	}
	preview, err := client.GetProjectEnvironmentPromotionPreviewWithConfig(ctx, projectSlug, toEnvironment, fromEnvironment, syncConfig)
	if err != nil {
		return projectEnvironmentPreflightResponse{}, fmt.Errorf("qualification %s was recorded, but promotion preview failed: %w", qualification.ID, err)
	}

	result := projectEnvironmentPreflightResponse{
		ProjectSlug: projectSlug, FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		Status: "ready",
		Qualification: projectEnvironmentPreflightQualification{
			ID: qualification.ID, Status: qualification.Status, ReleaseSetID: qualification.ReleaseSetID, ExpiresAt: qualification.ExpiresAt,
		},
		PromotionPreview: projectEnvironmentPreflightPreview{
			CanPromote: preview.CanPromote, QualificationRequired: preview.QualificationRequired,
			ApprovalRequired: preview.ApprovalRequired, SyncConfig: preview.SyncConfig,
		},
		BlockingReasons: append([]string(nil), preview.BlockingReasons...),
	}
	if qualification.Status != "passed" {
		result.BlockingReasons = appendUniqueReason(result.BlockingReasons, "source release qualification did not pass")
	}
	qualificationResolved := !preview.QualificationRequired || (preview.Qualification != nil &&
		preview.Qualification.ID == qualification.ID && preview.Qualification.Status == "passed")
	if !qualificationResolved {
		result.BlockingReasons = appendUniqueReason(result.BlockingReasons, "promotion preview did not resolve this passing qualification")
	}
	if !preview.CanPromote || qualification.Status != "passed" || !qualificationResolved {
		result.Status = "blocked"
	}
	return result, nil
}

func appendUniqueReason(reasons []string, reason string) []string {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}
