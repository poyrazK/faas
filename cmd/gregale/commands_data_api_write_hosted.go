package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed data_api_write_inspect_task.js
var dataAPIWriteInspectTask string

type dataAPIWriteRole struct {
	Name      string `json:"name"`
	Database  string `json:"database"`
	Superuser *bool  `json:"superuser"`
	BypassRLS *bool  `json:"bypass_rls"`
}
type dataAPIWriteRelation struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	RLS               *bool  `json:"rls"`
	ForceRLS          *bool  `json:"force_rls"`
	OwnerMember       *bool  `json:"owner_member"`
	RowSecurityActive *bool  `json:"row_security_active"`
	SchemaUsage       *bool  `json:"schema_usage"`
	Select            *bool  `json:"can_select"`
	Insert            *bool  `json:"can_insert"`
	Update            *bool  `json:"can_update"`
	Delete            *bool  `json:"can_delete"`
}
type dataAPIWriteColumn struct {
	Name      string `json:"name"`
	Generated *bool  `json:"generated"`
	Identity  *bool  `json:"identity"`
	Select    *bool  `json:"can_select"`
	Insert    *bool  `json:"can_insert"`
	Update    *bool  `json:"can_update"`
}
type dataAPIWritePolicy struct {
	Name          string `json:"name"`
	Command       string `json:"command"`
	Permissive    *bool  `json:"permissive"`
	AppliesToRole *bool  `json:"applies_to_role"`
	HasUsing      *bool  `json:"has_using"`
	HasCheck      *bool  `json:"has_check"`
}
type dataAPIWriteTrigger struct {
	Name    string `json:"name"`
	Enabled string `json:"enabled"`
	Type    int    `json:"type"`
}
type dataAPIWriteObservation struct {
	Version   int                   `json:"version"`
	Role      dataAPIWriteRole      `json:"role"`
	Relation  dataAPIWriteRelation  `json:"relation"`
	Columns   []dataAPIWriteColumn  `json:"columns"`
	Policies  []dataAPIWritePolicy  `json:"policies"`
	Triggers  []dataAPIWriteTrigger `json:"triggers"`
	Transport dataAPIWriteTransport `json:"transport"`
}
type dataAPIWriteTransport struct {
	Encrypted      *bool `json:"encrypted"`
	PeerAuthorized *bool `json:"peer_authorized"`
}
type dataAPIWriteBindingIdentity struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}
type dataAPIWriteHostedReport struct {
	Version              int                         `json:"version"`
	Status               string                      `json:"status"`
	AppID                string                      `json:"app_id"`
	DatabaseID           string                      `json:"database_id"`
	BindingID            string                      `json:"binding_id"`
	CredentialGeneration int64                       `json:"credential_generation"`
	DeploymentID         string                      `json:"deployment_id"`
	ConfigurationHash    string                      `json:"configuration_hash"`
	TaskID               string                      `json:"task_id"`
	Scope                dataAPIWriteQualification   `json:"scope"`
	Observation          dataAPIWriteObservation     `json:"observation"`
	Findings             []string                    `json:"findings"`
	Authorization        string                      `json:"authorization"`
	BindingDelivery      string                      `json:"binding_delivery"`
	BindingIdentity      dataAPIWriteBindingIdentity `json:"binding_identity"`
	Notes                []string                    `json:"notes"`
}

func dataAPIWriteValidName(s string) bool {
	return s != "" && len(s) <= 63 && utf8.ValidString(s) && !strings.ContainsRune(s, '\x00')
}
func dataAPIWritePresent(values ...*bool) bool {
	for _, value := range values {
		if value == nil {
			return false
		}
	}
	return true
}

func parseDataAPIWriteObservation(content string, table string) (dataAPIWriteObservation, error) {
	var result dataAPIWriteObservation
	bad := func() (dataAPIWriteObservation, error) {
		return dataAPIWriteObservation{}, errors.New("inspection returned incomplete or invalid metadata")
	}
	if len(content) == 0 || len(content) > 65536 {
		return bad()
	}
	unique := json.NewDecoder(strings.NewReader(content))
	if requalifyUniqueJSON(unique, 0) != nil {
		return bad()
	}
	if _, err := unique.Token(); err != io.EOF {
		return bad()
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil {
		return bad()
	}
	r := result.Relation
	if result.Version != 1 || !dataAPIWriteValidName(result.Role.Name) || !dataAPIWriteValidName(result.Role.Database) || !dataAPIWritePresent(result.Role.Superuser, result.Role.BypassRLS, result.Transport.Encrypted) || (*result.Transport.Encrypted && result.Transport.PeerAuthorized == nil) || (!*result.Transport.Encrypted && result.Transport.PeerAuthorized != nil) || r.Name != table || (r.Kind != "r" && r.Kind != "p") || !dataAPIWritePresent(r.RLS, r.ForceRLS, r.OwnerMember, r.RowSecurityActive, r.SchemaUsage, r.Select, r.Insert, r.Update, r.Delete) || len(result.Columns) == 0 || len(result.Columns) > 128 || result.Policies == nil || len(result.Policies) > 128 || result.Triggers == nil || len(result.Triggers) > 128 {
		return bad()
	}
	seen := map[string]bool{}
	for _, column := range result.Columns {
		if !dataAPIWriteValidName(column.Name) || seen[column.Name] || !dataAPIWritePresent(column.Generated, column.Identity, column.Select, column.Insert, column.Update) {
			return bad()
		}
		seen[column.Name] = true
	}
	seen = map[string]bool{}
	for _, policy := range result.Policies {
		if !dataAPIWriteValidName(policy.Name) || seen[policy.Name] || !slices.Contains([]string{"r", "a", "w", "d", "*"}, policy.Command) || !dataAPIWritePresent(policy.Permissive, policy.AppliesToRole, policy.HasUsing, policy.HasCheck) {
			return bad()
		}
		seen[policy.Name] = true
	}
	seen = map[string]bool{}
	for _, trigger := range result.Triggers {
		if !dataAPIWriteValidName(trigger.Name) || seen[trigger.Name] || !slices.Contains([]string{"O", "D", "R", "A"}, trigger.Enabled) || trigger.Type <= 0 || trigger.Type > 255 {
			return bad()
		}
		seen[trigger.Name] = true
	}
	return result, nil
}

func dataAPIWriteFindings(observation dataAPIWriteObservation, scope dataAPIWriteQualification) []string {
	result := []string{}
	r := observation.Relation
	if *observation.Role.Superuser || *observation.Role.BypassRLS {
		result = append(result, "role_bypasses_rls")
	}
	if *r.OwnerMember && !*r.ForceRLS {
		result = append(result, "table_owner_may_bypass_rls")
	}
	if !*r.RLS {
		result = append(result, "rls_disabled")
	}
	if *r.RLS && !*r.RowSecurityActive {
		result = append(result, "rls_inactive_for_observed_role")
	}
	if !*r.SchemaUsage {
		result = append(result, "schema_usage_missing")
	}
	if !*r.Select && (slices.Contains(scope.Operations, "update") || slices.Contains(scope.Operations, "delete")) {
		result = append(result, "select_table_grant_missing_review_columns")
	}
	for _, op := range scope.Operations {
		var granted bool
		var command string
		switch op {
		case "insert":
			granted, command = *r.Insert, "a"
		case "update":
			granted, command = *r.Update, "w"
		case "delete":
			granted, command = *r.Delete, "d"
		}
		if !granted {
			if op == "delete" {
				result = append(result, "delete_table_grant_missing")
			} else {
				result = append(result, op+"_table_grant_missing_review_columns")
			}
		}
		applicable := false
		permissive := false
		for _, p := range observation.Policies {
			if *p.AppliesToRole && (p.Command == command || p.Command == "*") {
				applicable = true
				if *p.Permissive {
					permissive = true
				}
			}
		}
		if *r.RLS && !applicable {
			result = append(result, op+"_applicable_policy_not_observed")
		} else if *r.RLS && !permissive {
			result = append(result, op+"_permissive_policy_not_observed")
		}
	}
	return result
}

type dataAPIWriteHostedClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	GetManagedPostgresDatabase(context.Context, string) (api.ManagedPostgresDatabase, error)
	GetManagedPostgresBinding(context.Context, string) (api.ManagedPostgresBinding, error)
	GetAppReleaseReview(context.Context, string) (api.AppReleaseReview, error)
	CreateAppTask(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error)
	GetAppTask(context.Context, string, string) (api.AppTaskResponse, error)
}
type dataAPIWriteHostedSnapshot struct {
	appID, databaseID, bindingID, deploymentID, configurationHash string
	generation                                                    int64
}

type dataAPIWriteInspectionError struct{ code string }

func (e dataAPIWriteInspectionError) Error() string { return e.code }

func observeDataAPIWriteBinding(ctx context.Context, client dataAPIWriteHostedClient, appSlug, databaseID, bindingID string) (dataAPIWriteHostedSnapshot, error) {
	var snapshot dataAPIWriteHostedSnapshot
	db, err := client.GetManagedPostgresDatabase(ctx, databaseID)
	if err != nil || db.ID != databaseID || db.State != "ready" || db.Hold != "" {
		return snapshot, errors.New("database unavailable")
	}
	app, err := client.GetApp(ctx, appSlug)
	if err != nil || app.ID == "" || app.Slug != appSlug {
		return snapshot, errors.New("app unavailable")
	}
	b, err := client.GetManagedPostgresBinding(ctx, bindingID)
	if err != nil || b.ID != bindingID {
		return snapshot, errors.New("writable binding unavailable")
	}
	// A complete control-plane record can disprove the selection. This says
	// nothing about which credentials an app task actually used.
	if b.DatabaseID != "" && b.DatabaseID != databaseID || b.AppID != "" && b.AppID != app.ID {
		return snapshot, dataAPIWriteInspectionError{"binding_selection_mismatch"}
	}
	if b.DatabaseID != databaseID || b.AppID != app.ID || b.Scope != api.DefaultEnvScope || b.EnvironmentKey != "DATABASE_URL" || b.Access != "data_api" || b.State != "ready" || b.CredentialGeneration < 1 || b.RotationPending {
		return snapshot, errors.New("writable binding unavailable")
	}
	review, err := client.GetAppReleaseReview(ctx, appSlug)
	if err != nil || review.AppID != app.ID || review.Scope != api.DefaultEnvScope || !dataAPIApprovalFingerprint(review.ConfigurationHash) || review.LatestDeploymentID == "" || len(review.ServingDeploymentIDs) != 1 || review.ServingDeploymentIDs[0] != review.LatestDeploymentID {
		return snapshot, errors.New("stable app deployment unavailable")
	}
	return dataAPIWriteHostedSnapshot{app.ID, databaseID, bindingID, review.LatestDeploymentID, review.ConfigurationHash, b.CredentialGeneration}, nil
}

func inspectDataAPIWriteHosted(ctx context.Context, client dataAPIWriteHostedClient, appSlug, databaseID, bindingID string, scope dataAPIWriteQualification) (dataAPIWriteHostedReport, error) {
	var result dataAPIWriteHostedReport
	validated, err := dataAPIWriteScope(scope.Schema, scope.Table, strings.Join(scope.Operations, ","))
	if err != nil || scope.Schema != "api" || !slices.Equal(validated.Operations, scope.Operations) || !api.ValidAppSlug(appSlug) || databaseID == "" || bindingID == "" {
		return result, errors.New("invalid inspection scope")
	}
	before, err := observeDataAPIWriteBinding(ctx, client, appSlug, databaseID, bindingID)
	if err != nil {
		var inspectionError dataAPIWriteInspectionError
		if errors.As(err, &inspectionError) {
			return result, inspectionError
		}
		return result, dataAPIWriteInspectionError{"binding_or_deployment_unavailable"}
	}
	command := []string{"node", "-e", dataAPIWriteInspectTask, scope.Table}
	request := api.CreateAppTaskRequest{Command: command, TimeoutSeconds: 60, MaxOutputBytes: 65536}
	if _, problem := request.Resolve(); problem != nil {
		return result, errors.New("invalid inspection task")
	}
	task, err := client.CreateAppTask(ctx, appSlug, request)
	if err != nil || task.ID == "" {
		return result, dataAPIWriteInspectionError{"inspection_task_unavailable"}
	}
	taskID := task.ID
	for !task.Status.Terminal() {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, dataAPIWriteInspectionError{"inspection_timed_out_or_cancelled"}
		case <-timer.C:
		}
		task, err = client.GetAppTask(ctx, appSlug, taskID)
		if err != nil {
			return result, dataAPIWriteInspectionError{"inspection_task_unavailable"}
		}
		if task.ID != taskID {
			return result, dataAPIWriteInspectionError{"inspection_task_changed"}
		}
	}
	if task.Status != api.AppTaskStatusSucceeded || task.ID != taskID || task.AppID != before.appID || task.DeploymentID != before.deploymentID || task.DeploymentScope != api.DefaultEnvScope || task.CommandShell || !slices.Equal(task.Command, command) || task.OutputTruncated || task.ExitCode == nil || *task.ExitCode != 0 {
		return result, dataAPIWriteInspectionError{"inspection_task_failed_or_incomplete"}
	}
	observation, err := parseDataAPIWriteObservation(task.StdoutTail, scope.Table)
	if err != nil {
		return result, dataAPIWriteInspectionError{"inspection_output_incomplete_or_invalid"}
	}
	after, err := observeDataAPIWriteBinding(ctx, client, appSlug, databaseID, bindingID)
	if err != nil || after != before {
		return result, dataAPIWriteInspectionError{"binding_or_deployment_changed"}
	}
	findings := dataAPIWriteFindings(observation, scope)
	return dataAPIWriteHostedReport{Version: 1, Status: "review_required", AppID: before.appID, DatabaseID: before.databaseID, BindingID: before.bindingID, CredentialGeneration: before.generation, DeploymentID: before.deploymentID, ConfigurationHash: before.configurationHash, TaskID: taskID, Scope: scope, Observation: observation, Findings: findings, Authorization: "not_tested", BindingDelivery: "not_independently_verified", BindingIdentity: dataAPIWriteBindingIdentity{Status: "unknown", Reason: "credential_and_database_target_not_independently_verified"}, Notes: []string{
		"The task used its DATABASE_URL; control-plane binding identity does not independently prove which credentials or database the task received.",
		"Transport observations come from the task's PostgreSQL client and do not establish that its peer is the selected managed database.",
		"Policy expressions, membership data, function bodies, trigger effects, sequences and actual application identities are not evaluated.",
		"Catalog observations are point-in-time and cannot establish tenant isolation, deployed replica identity or future behavior.",
	}}, nil
}

func printDataAPIWriteHostedReport(report dataAPIWriteHostedReport) int {
	exitCode := 0
	if report.Status != "observed" {
		exitCode = 1
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return printErr("Cannot write inspection", err)
		}
		return exitCode
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "Observed api.%s in task %s, deployment %s; role %q; status %s.\n", report.Scope.Table, report.TaskID, report.DeploymentID, report.Observation.Role.Name, report.Status)
	fmt.Fprintf(&out, "Table grants: SELECT=%t INSERT=%t UPDATE=%t DELETE=%t; RLS=%t forced=%t.\n", *report.Observation.Relation.Select, *report.Observation.Relation.Insert, *report.Observation.Relation.Update, *report.Observation.Relation.Delete, *report.Observation.Relation.RLS, *report.Observation.Relation.ForceRLS)
	fmt.Fprintf(&out, "Binding identity: %s (%s); connection encrypted: %t", report.BindingIdentity.Status, report.BindingIdentity.Reason, *report.Observation.Transport.Encrypted)
	if report.Observation.Transport.PeerAuthorized != nil {
		fmt.Fprintf(&out, "; peer certificate authorized: %t", *report.Observation.Transport.PeerAuthorized)
	}
	fmt.Fprintln(&out, ".")
	for _, column := range report.Observation.Columns {
		fmt.Fprintf(&out, "  column %q: SELECT=%t INSERT=%t UPDATE=%t generated=%t identity=%t\n", column.Name, *column.Select, *column.Insert, *column.Update, *column.Generated, *column.Identity)
	}
	for _, policy := range report.Observation.Policies {
		fmt.Fprintf(&out, "  policy %q: command=%s permissive=%t applies_to_role=%t using=%t check=%t\n", policy.Name, policy.Command, *policy.Permissive, *policy.AppliesToRole, *policy.HasUsing, *policy.HasCheck)
	}
	for _, trigger := range report.Observation.Triggers {
		fmt.Fprintf(&out, "  trigger %q: enabled=%s type=%d\n", trigger.Name, trigger.Enabled, trigger.Type)
	}
	for _, finding := range report.Findings {
		fmt.Fprintf(&out, "Review: %s\n", finding)
	}
	for _, note := range report.Notes {
		fmt.Fprintln(&out, note)
	}
	_, _ = fmt.Fprint(osStdout, out.String())
	return exitCode
}
