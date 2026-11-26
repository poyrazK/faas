package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIWriteQualification struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Operations []string `json:"operations"`
}

var dataAPIWriteIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func dataAPIWriteScope(schema, table, operations string) (dataAPIWriteQualification, error) {
	if !dataAPIWriteIdentifier.MatchString(schema) || !dataAPIWriteIdentifier.MatchString(table) {
		return dataAPIWriteQualification{}, errors.New("schema and table must be simple PostgreSQL identifiers")
	}
	chosen := map[string]bool{}
	for _, operation := range strings.Split(operations, ",") {
		if operation != "insert" && operation != "update" && operation != "delete" || chosen[operation] {
			return dataAPIWriteQualification{}, errors.New("operations must be unique comma-separated insert,update,delete values")
		}
		chosen[operation] = true
	}
	scope := dataAPIWriteQualification{Schema: schema, Table: table}
	for _, operation := range []string{"insert", "update", "delete"} {
		if chosen[operation] {
			scope.Operations = append(scope.Operations, operation)
		}
	}
	return scope, nil
}

type dataAPIWriteReadiness struct {
	Version          int                       `json:"version"`
	DatabaseID       string                    `json:"database_id"`
	Scope            dataAPIWriteQualification `json:"scope"`
	PreviewRole      string                    `json:"preview_role"`
	PreviewSuperuser bool                      `json:"preview_superuser"`
	PreviewBypassRLS bool                      `json:"preview_bypass_rls"`
	Relation         *dataAPICatalogRelation   `json:"relation"`
	CatalogStatus    string                    `json:"catalog_status"`
	WriteGrants      string                    `json:"prospective_binding_write_grants"`
	Policies         string                    `json:"effective_write_policies"`
	Authorization    string                    `json:"authorization"`
	Notes            []string                  `json:"notes"`
}

func dataAPIWriteReadinessReport(catalog dataAPICatalogPreview, scope dataAPIWriteQualification) dataAPIWriteReadiness {
	report := dataAPIWriteReadiness{Version: 1, DatabaseID: catalog.DatabaseID, Scope: scope, PreviewRole: catalog.Role, PreviewSuperuser: catalog.Superuser, PreviewBypassRLS: catalog.BypassRLS,
		CatalogStatus: "relation_not_found", WriteGrants: "unknown", Policies: "unknown", Authorization: "not_tested",
		Notes: []string{"Preview may create or refresh a provider-managed read-only login and wake database compute; no app or binding is created.",
			"The preview role is not the prospective writable binding; table and column write grants, applicable policies, triggers and role bypass are not assessed.",
			"Catalog metadata cannot establish authorization or whether a table is safe to expose. Review grants, policies and side effects separately.",
			"The disposable write check exercises local fixtures and identities only; it does not qualify the hosted database or enable writes."}}
	for _, relation := range catalog.Relations {
		if relation.Name == scope.Table {
			copy := relation
			report.Relation = &copy
			report.CatalogStatus = "observed"
			if relation.Kind != "r" && relation.Kind != "p" {
				report.CatalogStatus = "unsupported_relation_kind"
			}
			break
		}
	}
	if catalog.Partial {
		report.CatalogStatus = "incomplete"
	}
	return report
}

func cmdDataAPIConnectWriteReadiness(args []string) int {
	fs := newFlagSet("data-api write-readiness", flag.ContinueOnError)
	database := fs.String("database", "", "existing managed PostgreSQL database")
	app := fs.String("app", "", "existing Data API app with a writable binding")
	binding := fs.String("binding", "", "exact writable binding ID")
	schema := fs.String("schema", "", "schema (api for a selected binding)")
	table := fs.String("table", "", "table to inspect")
	operations := fs.String("operations", "", "comma-separated insert,update,delete")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	hosted := *app != "" || *binding != "" || *schema != ""
	requestedSchema := "public"
	if hosted {
		requestedSchema = *schema
	}
	scope, err := dataAPIWriteScope(requestedSchema, *table, *operations)
	if err != nil || fs.NArg() != 0 || *database == "" || (hosted && (*app == "" || *binding == "" || *schema != "api" || !api.ValidAppSlug(*app))) {
		return printErr("Invalid write-readiness options", errors.New("provide --database, --table and --operations; selected binding inspection also requires --app, --binding and --schema api"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if hosted {
		db, err := resolveManagedPostgresDatabase(ctx, client, *database)
		if err != nil {
			return printErr("Cannot resolve managed database", err)
		}
		if !jsonOutput {
			_, _ = fmt.Fprintln(osStdout, "Inspection starts a read-only app task and may wake compute; task access is required.")
		}
		report, err := inspectDataAPIWriteVerifiedHosted(ctx, client, *app, db.ID, *binding, scope)
		if err != nil {
			reason := "binding_or_inspection_unavailable"
			if errors.Is(err, errDataAPIInspectionUnsupported) {
				reason = "server_inspection_unsupported"
			}
			var inspectionError dataAPIWriteInspectionError
			if errors.As(err, &inspectionError) {
				reason = inspectionError.code
			}
			if jsonOutput {
				response := map[string]any{"version": 1, "status": "unavailable", "scope": scope, "authorization": "not_tested", "reason": reason}
				if reason == "binding_selection_mismatch" {
					response["binding_identity"] = dataAPIWriteBindingIdentity{Status: "mismatch", Reason: "binding_belongs_to_different_app_or_database"}
				} else {
					response["binding_identity"] = dataAPIWriteBindingIdentity{Status: "unknown", Reason: reason}
				}
				_ = writeJSON(response)
			} else if reason == "binding_selection_mismatch" {
				_, _ = fmt.Fprintln(osStderr, "Inspection unavailable: the selected binding belongs to a different app or database; no task was started.")
			} else {
				_, _ = fmt.Fprintf(osStderr, "Inspection unavailable (%s); verify the selected binding, task permissions and deployment, then retry.\n", reason)
			}
			return 1
		}
		return printDataAPIWriteHostedReport(report)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintln(osStdout, "Preview may wake database compute and create or refresh a provider-managed read-only login; no app or binding is created.")
	}
	catalog, err := readDataAPICatalogPreview(ctx, client, *database)
	if err != nil {
		return printErr("Catalog preview unavailable", err)
	}
	report := dataAPIWriteReadinessReport(catalog, scope)
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return printErr("Cannot write readiness report", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "public.%s: %s; requested operations: %s.\n", scope.Table, report.CatalogStatus, strings.Join(scope.Operations, ", "))
		if report.Relation != nil {
			_, _ = fmt.Fprintf(osStdout, "Preview role %q (superuser=%t, bypass_rls=%t): kind=%s, RLS=%t, forced=%t, policies=%d.\n", report.PreviewRole, report.PreviewSuperuser, report.PreviewBypassRLS, report.Relation.Kind, report.Relation.RLS, report.Relation.ForceRLS, report.Relation.PolicyCount)
		}
		_, _ = fmt.Fprintln(osStdout, "Future binding grants and effective write policies: unknown; authorization: not tested.")
		for _, note := range report.Notes {
			_, _ = fmt.Fprintln(osStdout, note)
		}
	}
	if report.CatalogStatus != "observed" {
		return 1
	}
	return 0
}

func cmdDataAPIConnectWriteCheck(args []string) int {
	fs := newFlagSet("data-api write-check", flag.ContinueOnError)
	directory := fs.String("directory", ".", "local Data API project")
	config := fs.String("config", "", "local development config")
	checks := fs.String("access-checks", "", "reviewed version 4 mutation manifest")
	fixture := fs.String("fixture", "", "synthetic SQL fixture")
	schema := fs.String("schema", "", "schema in the local disposable project")
	table := fs.String("table", "", "table in the local disposable project")
	operations := fs.String("operations", "", "comma-separated insert,update,delete")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	scope, err := dataAPIWriteScope(*schema, *table, *operations)
	if err != nil || scope.Schema != "api" || fs.NArg() != 0 || *directory == "" || *checks == "" || *fixture == "" {
		return printErr("Invalid write-check options", errors.New("provide --schema api, --table, --operations, --access-checks and an explicit synthetic --fixture; local verification exposes only api"))
	}
	devArgs := []string{"--directory", *directory, "--once", "--port", "0"}
	if *config != "" {
		devArgs = append(devArgs, "--config", *config)
	}
	return runDataAPIDev(devArgs, &dataAPIMigrationOptions{Verify: true, AccessChecks: *checks, Fixture: *fixture, WriteQualification: &scope})
}
