package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func verificationJSON(path string, target any, limit int64) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return errors.New("cannot read regular input file")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return errors.New("input exceeds bounds")
	}
	if json.Unmarshal(raw, target) != nil {
		return errors.New("invalid input JSON")
	}
	return nil
}
func cmdDataAPIVerifyInit(args []string) int {
	fs := newFlagSet("data-api verify init", flag.ContinueOnError)
	schema := fs.String("schema", "", "saved schema contract")
	config := fs.String("config", "", "development config containing identity names")
	output := fs.String("output", "", "new verification package directory")
	plans := fs.String("plans", "", "successful explain JSON report")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *schema == "" || *config == "" || *output == "" {
		return printErr("Invalid verification init", errors.New("provide --schema FILE --config FILE --output NEW_DIR"))
	}
	if err := buildVerificationPackage(*schema, *config, *plans, *output); err != nil {
		return printErr("Verification init failed", err)
	}
	if jsonOutput {
		raw, _ := json.Marshal(map[string]any{"command": "data-api verify init", "status": "needs_review", "output": *output})
		if _, err := fmt.Fprintln(osStdout, string(raw)); err != nil {
			return printErr("Output failed", err)
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "Verification templates created in %s; review coverage.json and README.md before running checks.\n", *output); err != nil {
			return printErr("Output failed", err)
		}
	}
	return 0
}
func buildVerificationPackage(schemaPath, configPath, plansPath, output string) error {
	contract, err := readDataAPIContractFile(schemaPath)
	if err != nil {
		return errors.New("use a valid saved schema contract")
	}
	var config struct {
		Version    int                        `json:"version"`
		Identities map[string]json.RawMessage `json:"identities"`
	}
	if err := verificationJSON(configPath, &config, 65536); err != nil {
		return err
	}
	names := []string{}
	validName := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	for name := range config.Identities {
		if !validName.MatchString(name) {
			return errors.New("invalid configured identity name")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if config.Version != 1 || len(names) < 2 || len(names) > 100 {
		return errors.New("use a version 1 config with at least two identity names")
	}
	quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
	checks := []map[string]any{}
	scenarios := []map[string]any{}
	coverage := []map[string]any{}
	for _, table := range contract.Parsed.Tables {
		relation := table.Schema + "." + table.Name
		generated := 0
		if !table.View {
			for _, column := range table.Columns {
				if !column.Nullable && len(checks) < 100 {
					checks = append(checks, map[string]any{"name": fmt.Sprintf("Required field %d", len(checks)+1), "sql": "SELECT NOT EXISTS(SELECT 1 FROM " + quote(table.Schema) + "." + quote(table.Name) + " WHERE " + quote(column.Name) + " IS NULL) AS passed"})
					generated++
				}
			}
		}
		for _, sql := range verificationForeignKeys(table, contract.Parsed.Tables) {
			if len(checks) >= 100 {
				break
			}
			checks = append(checks, map[string]any{"name": fmt.Sprintf("Relationship integrity %d", len(checks)+1), "sql": sql})
			generated++
		}
		entry := map[string]any{"relation": relation, "integrity_templates": generated, "access": "needs_fixture_and_expectations", "gaps": []string{"Review tenant ownership, admin permissions and membership revocation", "Business invariants, SQL CHECK constraints and unsupported FK match variants require custom assertions"}}
		if table.Schema == "api" && len(table.Columns) > 0 && len(scenarios) < 50 {
			path := "/rest/v1/" + url.PathEscape(table.Name) + "?" + url.QueryEscape(table.Columns[0].Name) + "=eq.{{fixture_value}}&select=*"
			scenarios = append(scenarios, map[string]any{"name": fmt.Sprintf("relation_%d", len(scenarios)+1), "requests": []map[string]any{
				{"name": "Owner reads fixture row", "identity": names[0], "method": "GET", "path": path, "expect": map[string]any{"status": 200, "json": []any{map[string]any{"replace_with_expected_row": true}}}},
				{"name": "Other identity cannot read fixture row", "identity": names[1], "method": "GET", "path": path, "expect": map[string]any{"status": 200, "json": []any{}}},
			}})
		} else {
			entry["access"] = "not_generated"
		}
		coverage = append(coverage, entry)
	}
	files := map[string]any{"coverage.json": map[string]any{"version": 1, "status": "needs_review", "schema_fingerprint": contract.Fingerprint, "relations": coverage, "query_budgets": "not_provided"}}
	if len(checks) > 0 {
		files["data-checks.json"] = map[string]any{"version": 1, "checks": checks}
	}
	if len(scenarios) > 0 {
		files["access-checks.json"] = map[string]any{"version": 2, "scenarios": scenarios}
	}
	if plansPath != "" {
		var report struct {
			Version     int    `json:"version"`
			Command     string `json:"command"`
			Status      string `json:"status"`
			Cleanup     string `json:"cleanup"`
			Fingerprint string `json:"schema_fingerprint"`
			Workload    string `json:"workload_fingerprint"`
			Dataset     string `json:"dataset_fingerprint"`
			Results     []struct {
				Scenario         string `json:"scenario"`
				QueryFingerprint string `json:"query_fingerprint"`
				Name             string `json:"name"`
				Status           string `json:"status"`
				Plan             struct {
					Cost *float64 `json:"total_cost"`
				} `json:"plan"`
				Metrics struct {
					Rows *float64 `json:"estimated_scan_rows"`
				} `json:"metrics"`
			} `json:"results"`
		}
		if err := verificationJSON(plansPath, &report, 1048576); err != nil {
			return err
		}
		hashPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
		if !hashPattern.MatchString(report.Workload) || !hashPattern.MatchString(report.Dataset) || report.Version != 1 || report.Command != "data-api explain" || report.Status != "passed" || report.Cleanup != "passed" || report.Fingerprint != contract.Fingerprint || len(report.Results) > 100 {
			return errors.New("plans must be a successful explain report matching the saved schema")
		}
		budgets := []map[string]any{}
		seen := map[string]bool{}
		for _, result := range report.Results {
			if result.Status == "skipped" {
				continue
			}
			key := result.Scenario + "\x00" + result.Name
			if !hashPattern.MatchString(result.QueryFingerprint) || result.Status != "planned" || result.Scenario == "" || result.Name == "" || len(result.Scenario) > 200 || len(result.Name) > 200 || seen[key] || result.Plan.Cost == nil || *result.Plan.Cost < 0 {
				return errors.New("invalid planned query")
			}
			seen[key] = true
			item := map[string]any{"scenario": result.Scenario, "name": result.Name, "max_total_cost": *result.Plan.Cost, "max_cost_ratio": 1}
			if result.Metrics.Rows != nil {
				if *result.Metrics.Rows < 0 {
					return errors.New("invalid scan estimate")
				}
				item["max_scanned_rows"] = *result.Metrics.Rows
			}
			budgets = append(budgets, item)
		}
		if len(budgets) == 0 {
			return errors.New("plans must contain a planned read")
		}
		files["budgets.json"] = map[string]any{"version": 1, "queries": budgets}
		files["coverage.json"].(map[string]any)["query_budgets"] = "generated_from_baseline_requires_review"
	}
	target, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("choose a new output directory; existing files are preserved")
	}
	// Create the final directory exclusively; never replace an existing package.
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(target)
		}
	}()
	for name, value := range files {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if len(raw) > 65536 {
			return errors.New("generated manifest exceeds 64 KiB; use a smaller schema or workload")
		}
		if err = os.WriteFile(filepath.Join(target, name), append(raw, '\n'), 0600); err != nil {
			return err
		}
	}
	readme := `# Review your verification templates

This package is a draft, not proof of coverage. Review coverage.json.
Required-field checks come from schema nullability. Known API foreign keys also have orphan checks. Add SQL CHECK,
count, total and other business invariants that matter to your application.
Access checks use the first two sorted configured identity names as suggestions;
choose actual owner, nonmember and admin identities. Replace {{fixture_value}}
with a concrete fixture value, choose the correct ownership filter, and replace
all expected owner rows. Templates deliberately fail until customized. Add
forbidden mutation checks with follow-up reads, admin checks and revocation flows.
Views, unsupported schemas and bounds can leave relations without access templates.
Generated budgets use exact baseline estimates and ratio 1; review tolerances.
Keep the original successful explain report for --baseline and use the same fixture.
No files imply verified coverage. Names and catalog metadata are included; claims
and query values from the explain report are not copied.

Run from your project (replace PACKAGE and FIXTURE):
gregale data-api verify --checks PACKAGE/data-checks.json --access-checks PACKAGE/access-checks.json --fixture FIXTURE
gregale data-api explain --collection REQUESTS --fixture FIXTURE --baseline PLANS --check --budget PACKAGE/budgets.json
Omit flags for manifests that were not generated. No checks were executed by init.
`
	if err := os.WriteFile(filepath.Join(target, "README.md"), []byte(readme), 0600); err != nil {
		return err
	}
	complete = true
	return nil
}

func verificationForeignKeys(table dataAPITable, tables []dataAPITable) []string {
	if table.View || table.Schema != "api" {
		return nil
	}
	var relationships []struct {
		Name          string   `json:"foreignKeyName"`
		Columns       []string `json:"columns"`
		Target        string   `json:"referencedRelation"`
		TargetColumns []string `json:"referencedColumns"`
	}
	if json.Unmarshal(table.Relationships, &relationships) != nil {
		return nil
	}
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	hasColumn := func(t dataAPITable, name string) bool {
		for _, column := range t.Columns {
			if column.Name == name {
				return true
			}
		}
		return false
	}
	result := []string{}
	for _, relationship := range relationships {
		if relationship.Name == "" || len(relationship.Columns) == 0 || len(relationship.Columns) != len(relationship.TargetColumns) {
			continue
		}
		var target *dataAPITable
		for i := range tables {
			if tables[i].Schema == "api" && tables[i].Name == relationship.Target {
				target = &tables[i]
				break
			}
		}
		if target == nil {
			continue
		}
		valid := true
		nonNull := []string{}
		joins := []string{}
		for i, column := range relationship.Columns {
			referenced := relationship.TargetColumns[i]
			if !hasColumn(table, column) || !hasColumn(*target, referenced) {
				valid = false
				break
			}
			nonNull = append(nonNull, "source."+quote(column)+" IS NOT NULL")
			joins = append(joins, "target."+quote(referenced)+"=source."+quote(column))
		}
		if valid {
			result = append(result, "SELECT NOT EXISTS(SELECT 1 FROM "+quote("api")+"."+quote(table.Name)+" source WHERE "+strings.Join(nonNull, " AND ")+" AND NOT EXISTS(SELECT 1 FROM "+quote("api")+"."+quote(target.Name)+" target WHERE "+strings.Join(joins, " AND ")+")) AS passed")
		}
	}
	return result
}
