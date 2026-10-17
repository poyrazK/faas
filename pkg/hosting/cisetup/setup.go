// Package cisetup renders and checks a deliberately narrow GitHub workflow
// contract. It never fetches dependencies, invokes a shell or contacts GitHub.
package cisetup

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var pathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
var cronPattern = regexp.MustCompile(`^[0-9*/ ,\-]+$`)

func Prepare(site api.HostingSite, trust api.HostingOIDCTrust, caps api.HostingCapabilities, req api.HostingCISetupRequest, now time.Time) api.HostingCISetup {
	out := api.HostingCISetup{SiteID: site.ID, TrustID: trust.ID, Repository: trust.Repository, ObservedAt: now, Checks: []api.HostingCISetupCheck{}, Recovery: []string{
		"Review the preparation Action: it runs with the job's contents:read and id-token:write permissions and must provide the pinned CLI and trusted prebuilt output.",
		"Reruns preserve the original CI run ID and preview expiry. Keep files, source labels and preview TTL unchanged; never change an ID to hide an uncertain failure.",
		"Inspect Action outputs for run-id, release-id and preview-id. Use gregale sites diagnose with the original site and run IDs; diagnostics needs its independent opt-ins.",
		"Generation and preflight do not verify remote commit contents, binary provenance, GitHub defaults, secrets, provider setup or serving health.",
	}}
	add := func(code string, pass bool, message string) {
		state := "pass"
		if !pass {
			state = "blocked"
		}
		out.Checks = append(out.Checks, api.HostingCISetupCheck{Code: code, State: state, Message: message})
	}
	idValid := func(s string) bool { id, err := uuid.Parse(s); return err == nil && id.String() == s && id != uuid.Nil }
	add("site", idValid(site.ID) && site.ID == trust.SiteID && site.AccountID == trust.AccountID && site.Status == "active" && !site.Suspended && site.DurableRouting, "Use an active, unsuspended durable site belonging to the selected trust.")
	add("trust", idValid(trust.ID) && trust.ID == req.TrustID && hosting.ValidHostingOIDCTrust(trust.HostingOIDCTrustRules), "Select an existing immutable GitHub trust binding for this site.")
	add("features", caps.CISetupEnabled && caps.OIDCEnabled && caps.AutomationEnabled && caps.PreviewsEnabled && caps.HistoryEnabled && caps.StagedReleasesSupported, "Enable setup, OIDC, automation, previews, history and durable hosting before using this configuration.")
	branch := strings.TrimPrefix(trust.Ref, "refs/heads/")
	safeIdentity := branchPattern.MatchString(branch) && !strings.Contains(trust.Environment, "${{") && !strings.ContainsAny(trust.Environment, "\x00\r\n")
	if req.Config.ReservePreview {
		add("preview_reservations", caps.PreviewReservationsEnabled, "Enable preview reservations before exporting reservation-backed CI admission.")
	}
	add("identity", safeIdentity, "Workflow export supports literal branch names containing letters, digits, dots, underscores, slashes and dashes; environment names cannot contain GitHub expressions.")
	out.WorkflowPath = strings.TrimSuffix(strings.TrimPrefix(trust.WorkflowRef, trust.Repository+"/"), "@"+trust.Ref)
	c := req.Config
	add("api_origin", validOrigin(c.APIBase), "Use a credential-free HTTPS API origin; verify it is Gregale's trusted identity-exchange endpoint.")
	add("action_pin", commitPattern.MatchString(c.ActionCommit) && c.ActionCommit != strings.Repeat("0", 40), "Provide a reviewed full lowercase 40-character hosting Action commit SHA.")
	add("checkout_pin", commitPattern.MatchString(c.CheckoutCommit) && c.CheckoutCommit != strings.Repeat("0", 40), "Provide a reviewed full lowercase 40-character actions/checkout commit SHA.")
	add("cli_pin", digestPattern.MatchString(c.CLISHA256) && c.CLISHA256 != strings.Repeat("0", 64), "Provide the SHA-256 of the reviewed Gregale CLI binary; the workflow checks it before executing the CLI.")
	add("cli_path", validPath(strings.TrimPrefix(c.CLIPath, "/")) && (strings.HasPrefix(c.CLIPath, "/") || strings.HasPrefix(c.CLIPath, "./")), "Provide an absolute or ./relative POSIX CLI file path without expressions, traversal or unresolved placeholders.")
	add("prepare_action", strings.HasPrefix(c.PrepareAction, "./.github/actions/") && len(c.PrepareAction) > len("./.github/actions/") && validPath(c.PrepareAction), "Provide a reviewed local composite preparation Action under ./.github/actions/; it supplies the pinned CLI and built files.")
	add("output_path", !strings.HasPrefix(c.OutputPath, "/") && validPath(c.OutputPath), "Provide a relative prebuilt output directory, without expressions, traversal or unresolved placeholders.")
	add("lifetimes", c.PreviewTTLSeconds >= int64(api.HostingPreviewTTLMin/time.Second) && c.PreviewTTLSeconds <= int64(api.HostingPreviewTTLMax/time.Second) && c.WaitTimeoutSeconds > 0 && c.WaitTimeoutSeconds <= int64(api.HostingCIWaitMax/time.Second), "Preview TTL and publication wait must stay within existing CI limits; retries keep the original TTL.")
	add("event", validEvent(trust.EventName, branch, c), "Push and workflow_dispatch accept no schedule settings. Schedule needs a fixed minute or */5..59 cadence and a declared default branch matching the trust.")
	out.ConfigurationValid = allPass(out.Checks)
	if !out.ConfigurationValid {
		return out
	}
	encoded, err := yaml.Marshal(buildWorkflow(trust, c))
	if err != nil {
		add("render", false, "Workflow rendering failed; refresh before exporting.")
		out.ConfigurationValid = false
		return out
	}
	if out.ConfigurationValid {
		out.WorkflowYAML = "# Generated Gregale internal frontend preview workflow. Review before committing.\n# Save at " + out.WorkflowPath + " in " + trust.Repository + ".\n# The reviewed local preparation Action supplies the pinned CLI and prebuilt output.\n" + string(encoded)
		if len(out.WorkflowYAML) > api.HostingCISetupWorkflowMaxBytes {
			add("render_size", false, "Generated workflow exceeds the bounded preflight format; shorten configuration values.")
			out.WorkflowYAML = ""
			out.ConfigurationValid = false
			return out
		}
		out.Checks = append(out.Checks, api.HostingCISetupCheck{Code: "preparation_contract", State: "manual", Message: "Preparation Action contents, actual CLI/output availability, remote commit contents and live service qualification require review; no remote checks were performed."})
	}
	return out
}

func allPass(checks []api.HostingCISetupCheck) bool {
	for _, c := range checks {
		if c.State == "blocked" {
			return false
		}
	}
	return true
}
func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && !strings.ContainsAny(raw, "?#") && u.Fragment == "" && u.Opaque == "" && !strings.Contains(raw, "${{") && !placeholder(raw)
}
func placeholder(s string) bool {
	upper := strings.ToUpper(s)
	return strings.ContainsAny(s, "<>") || strings.Contains(upper, "REPLACE_ME") || strings.Contains(upper, "YOUR_") || strings.Contains(upper, "TODO_")
}
func validPath(raw string) bool {
	if len(raw) == 0 || len(raw) > api.HostingCISetupPathMaxBytes || !pathPattern.MatchString(raw) || placeholder(raw) {
		return false
	}
	raw = strings.TrimPrefix(raw, "./")
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func validEvent(event, branch string, c api.HostingCISetupConfig) bool {
	if event != "schedule" {
		return (event == "push" || event == "workflow_dispatch") && c.Cron == "" && c.DefaultBranch == ""
	}
	if c.DefaultBranch != branch || len(c.Cron) > api.HostingCISetupCronMaxBytes || !cronPattern.MatchString(c.Cron) || len(strings.Fields(c.Cron)) != 5 {
		return false
	}
	if _, err := cron.ParseStandard(c.Cron); err != nil {
		return false
	}
	minute := strings.Fields(c.Cron)[0]
	if strings.HasPrefix(minute, "*/") {
		raw := strings.TrimPrefix(minute, "*/")
		number, err := strconv.Atoi(raw)
		return err == nil && strconv.Itoa(number) == raw && number >= 5 && number <= 59
	}
	number, err := strconv.Atoi(minute)
	return err == nil && strconv.Itoa(number) == minute && number >= 0 && number <= 59
}
