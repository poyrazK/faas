package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func resumeRecordedRun(kind string, args []string) int {
	fs := newFlagSet("resume accepted run", flag.ContinueOnError)
	readEvidence := new(bool)
	results := new(bool)
	savedJob := new(bool)
	savedWorkflow := new(bool)
	savedWorkflowPath := ""
	latest := new(bool)
	dryRun := new(bool)
	fs.BoolVar(dryRun, "dry-run", false, "preview the selected run and read action offline without waiting or writing files")
	name := new(string)
	app := new(string)
	nameFlag := "workflow"
	if kind == "job" {
		nameFlag = "job"
		fs.BoolVar(savedJob, "saved-job", false, "filter by the Job name in local jobs defaults; does not inherit run settings")
		fs.BoolVar(latest, "latest", false, "select the most recently recorded saved Job run on the current profile/API; requires --saved-job")
		fs.BoolVar(readEvidence, "logs", false, "read current logs for failed tasks in the exact recorded run")
		fs.BoolVar(results, "results", false, "wait for the recorded run to succeed and collect its artifacts")
	} else {
		fs.BoolVar(savedWorkflow, "saved-workflow", false, "use the workflow name and recorded app from local workflow recovery defaults")
		fs.BoolVar(latest, "latest", false, "select the most recently recorded workflow run on the current profile/API; requires --workflow and --app or --saved-workflow")
		fs.StringVar(app, "app", "", "filter workflow records by exact recorded app slug on the current profile/API")
		fs.BoolVar(readEvidence, "diagnose", false, "read diagnostics for the exact recorded workflow run")
	}
	fs.StringVar(name, nameFlag, "", "filter local records by exact workload name on the current profile/API")
	runID := fs.String("run", "", "locally recorded run UUID or unambiguous UUID prefix (at least 8 characters)")
	list := fs.Bool("list", false, "list local records without API access")
	overview := fs.Bool("overview", false, "read recorded run statuses; retain all local records")
	currentConnection := fs.Bool("current-connection", false, "restrict --list or --overview to the current profile and API")
	forget := fs.String("forget", "", "remove one exact local record without changing remote execution")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum read-only wait")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval (1s to 1h)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	runSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "run" {
			runSet = true
		}
	})
	if *timeout <= 0 || *interval < time.Second || *interval > time.Hour || runSet && !validRecordedRunPrefix(*runID) || *list && runSet {
		return printErr("Invalid resume options", errors.New("use --list or --run UUID/PREFIX (at least 8 lowercase UUID characters), a positive timeout, and a poll interval between 1s and 1h"))
	}
	modes := 0
	for _, active := range []bool{*list, *overview, *forget != ""} {
		if active {
			modes++
		}
	}
	forgetSet, timeoutSet, intervalSet, nameSet := false, false, false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case nameFlag:
			nameSet = true
		case "forget":
			forgetSet = true
		case "timeout":
			timeoutSet = true
		case "poll-interval":
			intervalSet = true
		}
	})
	if *savedJob && (nameSet || forgetSet) {
		return printErr("Invalid saved Job selector", errors.New("use --saved-job separately from --job and --forget"))
	}
	appSet := logsFlagWasSet(fs, "app")
	if *savedWorkflow && (nameSet || appSet || forgetSet) {
		return printErr("Invalid saved workflow selector", errors.New("use --saved-workflow separately from --workflow, --app, and --forget"))
	}
	if *latest && kind == "job" && (!*savedJob || runSet || *list || *overview || forgetSet || *currentConnection) {
		return printErr("Invalid latest run selector", errors.New("--latest requires --saved-job and excludes --run, --list, --overview, --forget, and --current-connection"))
	}
	if *latest && kind == "workflow" && (!*savedWorkflow && (!nameSet || !appSet) || runSet || *list || *overview || forgetSet || *currentConnection) {
		return printErr("Invalid latest run selector", errors.New("--latest requires --workflow NAME and --app SLUG or --saved-workflow, and excludes --run, --list, --overview, --forget, and --current-connection"))
	}
	if *dryRun && (*list || *overview || forgetSet || *currentConnection) {
		return printErr("Invalid resume preview options", errors.New("--dry-run previews recorded recovery; use it separately from --list, --overview, --forget, and --current-connection"))
	}
	if *currentConnection && !*list && !*overview {
		return printErr("Invalid connection filter", errors.New("--current-connection requires --list or --overview; recovery and --forget keep their existing selectors"))
	}
	if nameSet && (strings.TrimSpace(*name) == "" || kind == "job" && !jobSlugPattern.MatchString(*name) || kind == "workflow" && len(*name) > api.WorkflowWebhookNameMaxBytes) {
		return printErr("Invalid workload selector", errors.New("use a nonempty exact saved workload name; Job names must be valid slugs"))
	}
	if nameSet && forgetSet {
		return printErr("Invalid record maintenance options", errors.New("--forget accepts only an exact UUID; workload name filters apply to recovery, --list, and --overview"))
	}
	if appSet && !api.ValidAppSlug(*app) {
		return printErr("Invalid recorded app selector", errors.New("--app requires a valid nonempty recorded app slug; no remote app lookup is performed"))
	}
	if appSet && forgetSet {
		return printErr("Invalid record maintenance options", errors.New("--app filters recovery, --list, and --overview; --forget accepts only an exact UUID"))
	}
	if modes > 1 || modes > 0 && *runID != "" || forgetSet && !jobRunIDPattern.MatchString(*forget) || (*list || forgetSet) && (timeoutSet || intervalSet) || *overview && intervalSet {
		return printErr("Invalid record maintenance options", errors.New("use --list, --overview [--timeout DURATION], or --forget UUID separately; --run and --poll-interval are for resuming observation"))
	}
	if *readEvidence && (modes > 0 || timeoutSet || intervalSet) {
		return printErr("Invalid evidence options", errors.New("--logs or --diagnose accepts workload name and --run UUID/PREFIX selectors; observation, maintenance, and timing options must be used separately"))
	}
	if *results && (modes > 0 || *readEvidence) {
		return printErr("Invalid result collection options", errors.New("--results accepts workload name, --run UUID/PREFIX, and wait timing flags; logs and record maintenance modes must be used separately"))
	}
	if *savedJob {
		defaults, _, err := readJobDefaults()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return printErr("No saved Job defaults", errors.New("configure jobs defaults set JOB, or use --job NAME explicitly; no API request was made"))
			}
			return printErr("Could not read saved Job defaults", err)
		}
		*name, nameSet = defaults.Job, true
	}
	if *savedWorkflow {
		defaults, path, err := readWorkflowDefaults()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return printErr("No saved workflow defaults", errors.New("configure workflows defaults set NAME --app SLUG, or use --workflow NAME --app SLUG explicitly; no API request was made"))
			}
			return printErr("Could not read saved workflow defaults", err)
		}
		*name, *app, nameSet, appSet, savedWorkflowPath = defaults.Workflow, defaults.App, true, true, path
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using workflow recovery defaults from %s: workflow=%s; app=%s.\n", oneLine(path), oneLine(*name), *app)
		}
	}
	records, err := loadAcceptedRunRecords(kind)
	if err != nil {
		return printErr("Could not read accepted runs", err)
	}
	if *currentConnection {
		profile, endpoint := currentProfile(), apiBase()
		filtered := make([]acceptedRunRecord, 0, len(records))
		for _, record := range records {
			if record.Profile == profile && record.API == endpoint {
				filtered = append(filtered, record)
			}
		}
		records = filtered
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Saved-run connection filter: profile=%s; API=%s; matching records=%d (local comparison only).\n", oneLine(profile), oneLine(endpoint), len(records))
		}
	}
	if appSet {
		filtered := make([]acceptedRunRecord, 0, len(records))
		availableApps := map[string]struct{}{}
		profile, endpoint := currentProfile(), apiBase()
		for _, record := range records {
			if record.Profile != profile || record.API != endpoint {
				continue
			}
			if record.App != "" {
				availableApps[record.App] = struct{}{}
			}
			if record.App == *app {
				filtered = append(filtered, record)
			}
		}
		if len(filtered) == 0 && !*list && !*overview {
			if *savedWorkflow {
				return printErr("No recorded runs for saved workflow", errors.New("no local runs match the saved app on the current profile/API; inspect workflows defaults show or workflows resume-wait --saved-workflow --list; no API request was made"))
			}
			return unknownSavedName("Unknown recorded app", *app, availableApps, func(candidate string) []string {
				return correctedSavedNameFlags([]string{"workflows", "resume-wait"}, fs, candidate, "app")
			}, []string{"workflows", "resume-wait", "--list", "--current-connection"})
		}
		records = filtered
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Saved-run app filter: app=%s; profile=%s; API=%s; matching records=%d (local comparison only).\n", *app, oneLine(profile), oneLine(endpoint), len(records))
		}
	}
	if nameSet {
		filtered := make([]acceptedRunRecord, 0, len(records))
		availableNames := map[string]struct{}{}
		for _, record := range records {
			if record.Profile != currentProfile() || record.API != apiBase() {
				continue
			}
			availableNames[record.Name] = struct{}{}
			if record.Name == *name {
				filtered = append(filtered, record)
			}
		}
		if len(filtered) == 0 && !*list && !*overview {
			if *savedWorkflow {
				return printErr("No recorded runs for saved workflow", errors.New("no local runs match the saved workflow and app on the current profile/API; inspect workflows defaults show or workflows resume-wait --saved-workflow --list; no API request was made"))
			}
			if *savedJob {
				return printErr("No recorded runs for saved Job", fmt.Errorf("no local runs match Job %q on the current profile/API; inspect jobs defaults show or jobs resume --list; no API request was made", *name))
			}
			workload, prefix := "Job", []string{"jobs", "resume"}
			if kind == "workflow" {
				workload, prefix = "Workflow", []string{"workflows", "resume-wait"}
			}
			inspect := append(append([]string{}, prefix...), "--list")
			if appSet {
				inspect = append(inspect, "--app", *app)
			}
			return unknownSavedName("Unknown recorded "+workload, *name, availableNames, func(candidate string) []string {
				return correctedSavedNameFlags(prefix, fs, candidate, nameFlag)
			}, inspect)
		}
		records = filtered
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Saved-run filter: %s=%s; profile=%s; API=%s; matching records=%d (local comparison only).\n", nameFlag, oneLine(*name), oneLine(currentProfile()), oneLine(apiBase()), len(records))
		}
	}
	if *list {
		return renderRecordedRuns(records)
	}
	if *forget != "" {
		return forgetRecordedRun(kind, *forget, records)
	}
	if *overview {
		overviewTimeout := *timeout
		if !timeoutSet {
			overviewTimeout = 30 * time.Second
		}
		return overviewRecordedRuns(records, overviewTimeout)
	}
	candidates := records
	if *latest {
		record, err := latestRecordedRun(records)
		if err != nil {
			if code := renderRecordedRunChoices(records, fs); code != 0 {
				return code
			}
			return printErr("Could not select latest recorded run", err)
		}
		candidates = []acceptedRunRecord{record}
	}
	if *runID != "" {
		candidates = nil
		for _, record := range records {
			if strings.HasPrefix(record.RunID, *runID) && record.Profile == currentProfile() && record.API == apiBase() {
				candidates = append(candidates, record)
			}
		}
	}
	if len(candidates) != 1 {
		if runSet {
			if code := renderRecordedRunChoices(candidates, fs); code != 0 {
				return code
			}
			return printErr("Choose a recorded run", errors.New("the UUID or prefix must match exactly one saved run on the current profile/API; use a longer prefix or full UUID from the matching list; no API request was made"))
		}
		if code := renderRecordedRunChoices(records, fs); code != 0 {
			return code
		}
		return printErr("Choose a recorded run", errors.New("exactly one record is required; use --list, then --run UUID with the recorded connection profile and API address; no run was submitted"))
	}
	record := candidates[0]
	if runSet && len(*runID) < len(record.RunID) && !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Resolved local run prefix %s to %s.\n", *runID, record.RunID)
	}
	if record.Profile != currentProfile() || record.API != apiBase() {
		return printErr("Recorded connection differs", errors.New("select the recorded profile and API address before resuming; credentials and connection settings were not changed"))
	}
	if *dryRun {
		selection := "unique_local_record"
		if *latest {
			selection = "latest_recorded"
		} else if runSet {
			selection = "explicit_run"
		}
		if kind == "workflow" {
			return previewRecordedWorkflowAction(record, selection, *readEvidence, *timeout, *interval, savedWorkflowPath)
		}
		return previewRecordedJobAction(record, selection, *readEvidence, *results, *timeout, *interval)
	}
	if *latest {
		if jsonOutput {
			selection := struct {
				Event  string            `json:"event"`
				Record acceptedRunRecord `json:"record"`
			}{"latest_recorded_run_selected", record}
			if err := json.NewEncoder(osStderr).Encode(selection); err != nil {
				return printErr("Could not report selected run", err)
			}
		} else {
			workload := "Job"
			if kind == "workflow" {
				workload = "Workflow"
			}
			_, _ = fmt.Fprintf(osStderr, "Selected most recently recorded %s run: %s; %s=%s; recorded_at=%s.\n", workload, record.RunID, nameFlag, oneLine(record.Name), record.RecordedAt)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Resuming observation only: %s %s; run=%s; profile=%s; API=%s\n", kind, oneLine(record.Name), record.RunID, oneLine(record.Profile), oneLine(record.API))
	}
	if kind == "job" {
		if *results {
			return recordedJobReadAction(client, record, func() int {
				return cmdJobsResults([]string{record.Name, record.RunID, "--timeout", timeout.String(), "--poll-interval", interval.String()})
			})
		}
		if *readEvidence {
			return recordedJobFailureLogs(client, record)
		}
		return waitAcceptedJobRun(client, record.Name, api.JobRunResponse{ID: record.RunID, JobID: record.JobID, AccountID: record.AccountID}, *timeout, *interval)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	run, err := client.GetWorkflowRun(ctx, record.RunID)
	if err != nil {
		code := printErr("Could not read recorded workflow", err)
		if ctx.Err() == context.Canceled {
			return 130
		}
		if ctx.Err() == context.DeadlineExceeded {
			return 124
		}
		return code
	}
	if run.ID != record.RunID || run.WorkflowName != record.Name || record.AppID != "" && run.AppID != record.AppID {
		return printErr("Recorded workflow differs", errors.New("the server response did not match the recorded run identity; observation stopped"))
	}
	if *readEvidence {
		result, err := client.GetWorkflowRunDiagnostics(ctx, record.RunID)
		if err != nil {
			return printErr("Could not read recorded workflow diagnostics", err)
		}
		if result.RunID != record.RunID || result.WorkflowName != record.Name {
			return printErr("Recorded diagnostics differ", errors.New("diagnostics did not match the recorded run and workflow"))
		}
		if jsonOutput {
			return jsonOut(json.NewEncoder(osStdout).Encode(result))
		}
		renderWorkflowRunDiagnostics(osStdout, result)
		return 0
	}
	return waitWorkflowRun(client, record.RunID, *timeout, *interval, &run)
}

func resumeRecordedRunCLIFlags(kind string) []cliFlag {
	flags := []cliFlag{
		{Name: "dry-run", Bool: true, Short: "preview the selected run and action offline; no waiting, API access, or file writes"},
		{Name: "run", Value: "UUID|PREFIX", Short: "saved run UUID or unique prefix (at least 8 characters), resolved on the current profile/API"},
		{Name: "list", Bool: true, Short: "list saved identifiers without API access"},
		{Name: "overview", Bool: true, Short: "group recorded statuses into in progress, finished, and unavailable; retains records"},
		{Name: "current-connection", Bool: true, Short: "filter --list or --overview by current profile and API; retain all records"},
		{Name: "forget", Value: "UUID", Short: "remove one local record only; requires its recorded API/profile, makes no requests"},
		{Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m) or overview duration (default 30s); positive"},
		{Name: "poll-interval", Value: "DURATION", Short: "poll interval (default 2s; 1s to 1h)"},
	}
	if kind == "job" {
		flags = append(flags, cliFlag{Name: "job", Value: "NAME", Short: "filter saved runs by exact Job name on the current profile/API; supports --list and --overview"})
		flags = append(flags, cliFlag{Name: "saved-job", Bool: true, Short: "use the Job name from local jobs defaults; excludes --job and --forget; inherits no run settings"})
		flags = append(flags, cliFlag{Name: "latest", Bool: true, Short: "select the most recently recorded run for --saved-job on the current profile/API; excludes explicit run and maintenance selectors"})
		flags = append(flags, cliFlag{Name: "logs", Bool: true, Short: "read current failed-task logs from the exact recorded run; accepts --job and --run"})
		flags = append(flags, cliFlag{Name: "results", Bool: true, Short: "wait for success and collect exact recorded run outputs into ./results/JOB/RUN_ID"})
	} else {
		flags = append(flags, cliFlag{Name: "saved-workflow", Bool: true, Short: "use the workflow name and recorded app from local defaults; excludes --workflow, --app, and --forget"})
		flags = append(flags, cliFlag{Name: "latest", Bool: true, Short: "select the most recently recorded workflow/app run from explicit selectors or --saved-workflow on the current profile/API"})
		flags = append(flags, cliFlag{Name: "app", Value: "SLUG", Short: "filter local workflow records by exact recorded app on the current profile/API; supports --list and --overview"})
		flags = append(flags, cliFlag{Name: "workflow", Value: "NAME", Short: "filter saved runs by exact Workflow name on the current profile/API; supports --list and --overview"})
		flags = append(flags, cliFlag{Name: "diagnose", Bool: true, Short: "read exact recorded workflow diagnostics; accepts --workflow and --run"})
	}
	return flags
}

func latestRecordedRun(records []acceptedRunRecord) (acceptedRunRecord, error) {
	var selected acceptedRunRecord
	var newest time.Time
	tied := false
	for _, record := range records {
		if record.RecordedAt == "" {
			return selected, errors.New("matching older records lack recording times; use --list and an explicit --run UUID; no API request was made")
		}
		when, err := time.Parse(time.RFC3339Nano, record.RecordedAt)
		if err != nil {
			return selected, errors.New("invalid run recording time; no API request was made")
		}
		if selected.RunID == "" || when.After(newest) {
			selected, newest, tied = record, when, false
		} else if when.Equal(newest) {
			tied = true
		}
	}
	if selected.RunID == "" || tied {
		return selected, errors.New("no unique most recently recorded run; use --list and an explicit --run UUID; no API request was made")
	}
	return selected, nil
}

func validRecordedRunPrefix(prefix string) bool {
	if len(prefix) < 8 || len(prefix) > 36 {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if prefix[i] != '-' {
				return false
			}
		} else if strings.IndexByte("0123456789abcdef", prefix[i]) < 0 {
			return false
		}
	}
	return true
}

func recordedJobFailureLogs(client *api.Client, record acceptedRunRecord) int {
	return recordedJobReadAction(client, record, func() int {
		return cmdJobsFailedLogs([]string{record.Name, record.RunID})
	})
}

func recordedJobReadAction(client *api.Client, record acceptedRunRecord, action func() int) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	run, err := client.GetJobRun(ctx, record.Name, record.RunID)
	if err != nil {
		return printErr("Could not read recorded Job run", err)
	}
	if run.ID != record.RunID || run.JobID != record.JobID || run.AccountID != record.AccountID {
		return printErr("Recorded Job differs", errors.New("server response did not match the recorded Job and account identity"))
	}
	return action()
}

type recordedRunListing struct {
	acceptedRunRecord
	ConnectionMatches bool   `json:"connection_matches"`
	ResumeCommand     string `json:"resume_command"`
	SelectionCommand  string `json:"selection_command,omitempty"`
}

func renderRecordedRuns(records []acceptedRunRecord) int {
	return renderRecordedRunsWithSelection(records, nil)
}

// Selection suggestions retain the requested read action and replace any UUID
// prefix with the exact local identity. Other connections keep their existing
// observation guidance; invocation-specific flags are not transferred to them.
func renderRecordedRunChoices(records []acceptedRunRecord, fs *flag.FlagSet) int {
	return renderRecordedRunsWithSelection(records, func(record acceptedRunRecord) string {
		prefix := []string{"jobs", "resume"}
		if record.Kind == "workflow" {
			prefix = []string{"workflows", "resume-wait"}
		}
		args := correctedSavedNameFlags(prefix, fs, record.RunID, "run")
		if latest := fs.Lookup("latest"); latest != nil && latest.Value.String() == "true" {
			filtered := args[:0]
			for _, arg := range args {
				if !strings.HasPrefix(arg, "--latest=") {
					filtered = append(filtered, arg)
				}
			}
			args = filtered
		}
		runSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "run" {
				runSet = true
			}
		})
		if !runSet {
			args = append(args, "--run="+record.RunID)
		}
		return "FAAS_API=" + recoveryArgument(record.API) + " " + savedNameSuggestionCommand(args)
	})
}

func renderRecordedRunsWithSelection(records []acceptedRunRecord, selection func(acceptedRunRecord) string) int {
	rows := make([]recordedRunListing, 0, len(records))
	for _, record := range records {
		row := recordedRunListing{
			acceptedRunRecord: record,
			ConnectionMatches: record.Profile == currentProfile() && record.API == apiBase(),
			ResumeCommand:     recordedRunResumeCommand(record),
		}
		if selection != nil && row.ConnectionMatches {
			row.SelectionCommand = selection(record)
		}
		rows = append(rows, row)
	}
	if jsonOutput {
		return jsonOut(writeJSON(rows))
	}
	if len(records) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No locally recorded accepted runs match this listing. Existing explicit wait commands remain available.")
	}
	for _, row := range rows {
		record := row.acceptedRunRecord
		_, _ = fmt.Fprintf(osStdout, "%s %s: run=%s; app=%s; profile=%s; API=%s\n", record.Kind, oneLine(record.Name), record.RunID, oneLine(record.App), oneLine(record.Profile), oneLine(record.API))
		connection := "different from current connection"
		if row.ConnectionMatches {
			connection = "matches current connection"
		}
		_, _ = fmt.Fprintf(osStdout, "  Connection: %s (local comparison only).\n  Resume observation: %s\n", connection, row.ResumeCommand)
		if record.RecordedAt != "" {
			_, _ = fmt.Fprintf(osStdout, "  Recorded at: %s\n", record.RecordedAt)
		}
		if row.SelectionCommand != "" {
			_, _ = fmt.Fprintf(osStdout, "  Continue requested action (POSIX shells): %s\n", row.SelectionCommand)
		}
	}
	return 0
}
