package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// These allowlisted DTOs deliberately exclude configuration/variable values,
// secret fingerprints, and secret names from both text and JSON inspection.
type projectEnvironmentInspection struct {
	ProjectSlug      string                         `json:"project_slug"`
	Environment      string                         `json:"environment"`
	Protected        bool                           `json:"protected"`
	ConfigVersion    int64                          `json:"config_version"`
	ReleaseSetStatus string                         `json:"release_set_status"`
	ActiveReleaseSet *api.ProjectReleaseSetResponse `json:"active_release_set"`
	LastPromotion    *projectInspectionPromotion    `json:"last_promotion"`
	Workloads        []projectInspectionWorkload    `json:"workloads"`
	Issues           []projectInspectionIssue       `json:"issues"`
	BindingCoverage  []string                       `json:"binding_coverage"`
	RuntimeHealth    string                         `json:"runtime_health"`
	GeneratedAt      string                         `json:"generated_at"`
}

type projectInspectionPromotion struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	RollbackStatus string `json:"rollback_status,omitempty"`
}

type projectInspectionWorkload struct {
	Slug                string                     `json:"workload_slug"`
	LiveDeploymentID    string                     `json:"live_deployment_id,omitempty"`
	ReleaseDeploymentID string                     `json:"release_deployment_id,omitempty"`
	DeploymentStatus    string                     `json:"deployment_status"`
	URL                 string                     `json:"url,omitempty"`
	Bindings            []projectInspectionBinding `json:"bindings"`
}

type projectInspectionBinding struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Generation int64  `json:"credential_generation,omitempty"`
}

type projectInspectionIssue struct {
	Code         string `json:"code"`
	WorkloadSlug string `json:"workload_slug,omitempty"`
	Message      string `json:"message"`
}

func cmdProjectsEnvironmentInspect(args []string) int {
	project, environment, err := projectInspectionTarget(args)
	if err != nil {
		PrintUsage(os.Stderr, "usage: gregale projects environments inspect [<project> <environment>] (or use a linked project and environment)", "projects environments")
		return printErr("Invalid inspection target", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	snapshot, err := client.GetProjectEnvironmentState(context.Background(), project, environment)
	if err != nil {
		return printErr("Could not inspect project environment", err)
	}
	inspection := buildProjectEnvironmentInspection(snapshot)
	history, err := client.ListProjectEnvironmentPromotions(context.Background(), project, environment, "", 1, "", "")
	if err != nil {
		inspection.Issues = append(inspection.Issues, projectInspectionIssue{Code: "promotion_history_unavailable", Message: "Last promotion could not be read; retry inspection."})
	} else if len(history.Items) > 0 {
		last := history.Items[0]
		inspection.LastPromotion = &projectInspectionPromotion{ID: last.PromotionID, Status: last.Status, RollbackStatus: last.RollbackStatus}
	}
	if jsonOutput {
		return jsonOut(writeJSON(inspection))
	}
	renderProjectEnvironmentInspection(inspection)
	return 0
}

func projectInspectionTarget(args []string) (string, string, error) {
	if len(args) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		linked, _, err := linkedProjectContext(cwd)
		if err != nil {
			return "", "", err
		}
		args = []string{linked.Project, linked.Environment}
	}
	if len(args) != 2 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		return "", "", fmt.Errorf("supply a valid project and environment, or select both in the linked context")
	}
	return args[0], args[1], nil
}

func buildProjectEnvironmentInspection(snapshot api.ProjectEnvironmentStateResponse) projectEnvironmentInspection {
	out := projectEnvironmentInspection{
		ProjectSlug: snapshot.ProjectSlug, Environment: snapshot.Environment, Protected: snapshot.Protected,
		ConfigVersion: snapshot.Configuration.Version, ReleaseSetStatus: snapshot.ReleaseSetStatus,
		ActiveReleaseSet: snapshot.ActiveReleaseSet, GeneratedAt: snapshot.GeneratedAt,
		Workloads: []projectInspectionWorkload{}, Issues: []projectInspectionIssue{},
		BindingCoverage: []string{"managed_postgres", "object_storage"}, RuntimeHealth: "not_checked",
	}
	if out.ReleaseSetStatus == "" {
		out.ReleaseSetStatus = "unknown"
		out.Issues = append(out.Issues, projectInspectionIssue{Code: "release_inventory_unavailable", Message: "The server did not report release-set state."})
	}
	members := make(map[string]string)
	if snapshot.ActiveReleaseSet != nil {
		for _, member := range snapshot.ActiveReleaseSet.Members {
			members[member.AppID] = member.DeploymentID
		}
	}
	for _, workload := range snapshot.Workloads {
		selected := members[workload.AppID]
		delete(members, workload.AppID)
		out.Workloads = append(out.Workloads, inspectProjectWorkload(workload, selected))
		if snapshot.ActiveReleaseSet == nil {
			continue
		}
		if selected == "" {
			out.Issues = append(out.Issues, projectInspectionIssue{Code: "release_member_missing", WorkloadSlug: workload.WorkloadSlug, Message: "The active release has no deployment for this workload; publish a complete graph."})
		} else if selected != workload.Release.DeploymentID {
			out.Issues = append(out.Issues, projectInspectionIssue{Code: "release_selection_differs", WorkloadSlug: workload.WorkloadSlug, Message: "The environment URL's live deployment differs from the active release member."})
		}
	}
	if len(members) > 0 {
		out.Issues = append(out.Issues, projectInspectionIssue{Code: "release_members_detached", Message: "The active release contains members outside the current project inventory; publish a complete graph."})
	}
	sort.Slice(out.Workloads, func(i, j int) bool { return out.Workloads[i].Slug < out.Workloads[j].Slug })
	return out
}

func inspectProjectWorkload(workload api.ProjectEnvironmentStateWorkloadResponse, selected string) projectInspectionWorkload {
	out := projectInspectionWorkload{
		Slug: workload.WorkloadSlug, LiveDeploymentID: workload.Release.DeploymentID,
		ReleaseDeploymentID: selected, DeploymentStatus: workload.Release.Status, URL: workload.Release.URL,
		Bindings: []projectInspectionBinding{},
	}
	for _, binding := range workload.Bindings {
		out.Bindings = append(out.Bindings, projectInspectionBinding{Kind: binding.Kind, ID: binding.BindingID, Generation: binding.CredentialGeneration})
	}
	return out
}

func renderProjectEnvironmentInspection(in projectEnvironmentInspection) {
	_, _ = fmt.Fprintf(osStdout, "%s / %s\nProtected: %t\nConfiguration: v%d\n", in.ProjectSlug, in.Environment, in.Protected, in.ConfigVersion)
	release := in.ReleaseSetStatus
	if in.ActiveReleaseSet != nil {
		release = in.ActiveReleaseSet.ID
	}
	_, _ = fmt.Fprintf(osStdout, "Active release: %s\n", release)
	if in.LastPromotion != nil {
		_, _ = fmt.Fprintf(osStdout, "Last promotion: %s (%s; rollback=%s)\n", in.LastPromotion.ID, in.LastPromotion.Status, inspectionCell(in.LastPromotion.RollbackStatus))
	}
	_, _ = fmt.Fprintf(osStdout, "\n%-24s %-36s %-36s %s\n", "WORKLOAD", "RELEASE DEPLOYMENT", "ENVIRONMENT URL DEPLOYMENT", "RECORD STATUS")
	for _, workload := range in.Workloads {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-36s %-36s %s\n", workload.Slug, inspectionCell(workload.ReleaseDeploymentID), inspectionCell(workload.LiveDeploymentID), workload.DeploymentStatus)
		if workload.URL != "" {
			_, _ = fmt.Fprintf(osStdout, "  %s\n", workload.URL)
		}
		for _, binding := range workload.Bindings {
			_, _ = fmt.Fprintf(osStdout, "  binding: %s %s (generation %d)\n", binding.Kind, binding.ID, binding.Generation)
		}
	}
	_, _ = fmt.Fprintln(osStdout, "\nBinding coverage: managed PostgreSQL and object storage metadata only.")
	_, _ = fmt.Fprintln(osStdout, "Runtime health: not checked. A live deployment record does not establish runtime health.")
	for _, issue := range in.Issues {
		_, _ = fmt.Fprintf(osStdout, "Attention [%s] %s: %s\n", issue.Code, inspectionCell(issue.WorkloadSlug), issue.Message)
	}
}

func inspectionCell(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
