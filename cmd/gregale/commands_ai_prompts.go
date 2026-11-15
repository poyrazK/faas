// commands_ai_prompts.go — `gregale ai prompts` (ADR 20261114180740):
// managed prompts an app calls by name at $GREGALE_OUTBOUND_URL/p/<name>.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiPromptsUsage = "usage: gregale ai prompts list | get <name> [--version N] | push <file> [--live] | " +
	"diff <name> <version> <version> | render <name> [--version N] [--var key=value]... | promote <name> <version> | " +
	"rollback <name> | canary <name> <version> --percent N | canary <name> <promote|stop> | stats <name> | " +
	"evaluate <name> <version> --cases <file> --judge <integration-id> --judge-model <model> | evaluate show <evaluation-id> | " +
	"evaluate list <name> | rm <name>"

func cmdAIPrompts(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid ai prompts arguments", errors.New(aiPromptsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	cli := aiPromptsCLI{client: client, ctx: context.Background()}
	switch args[0] {
	case "list":
		return cli.list()
	case "get":
		return cli.get(args[1:])
	case "push":
		return cli.push(args[1:])
	case "diff":
		return cli.diff(args[1:])
	case "render":
		return cli.render(args[1:])
	case "promote":
		return cli.promote(args[1:])
	case "rollback":
		return cli.rollback(args[1:])
	case "canary":
		return cli.canary(args[1:])
	case "stats":
		return cli.stats(args[1:])
	case "evaluate":
		return cli.evaluate(args[1:])
	case "rm":
		return cli.remove(args[1:])
	}
	return printErr("Invalid ai prompts subcommand", errors.New(aiPromptsUsage))
}

type aiPromptsCLI struct {
	client interface {
		ListOutboundAIPrompts(context.Context) (api.OutboundAIPromptList, error)
		GetOutboundAIPrompt(context.Context, string) (api.OutboundAIPrompt, error)
		PushOutboundAIPromptVersion(context.Context, string, api.PushOutboundAIPromptVersionRequest) (api.PushOutboundAIPromptVersionResponse, error)
		ListOutboundAIPromptVersions(context.Context, string) (api.OutboundAIPromptVersionList, error)
		GetOutboundAIPromptVersion(context.Context, string, int) (api.OutboundAIPromptVersion, error)
		SetOutboundAIPromptLive(context.Context, string, int) (api.OutboundAIPrompt, error)
		RollBackOutboundAIPrompt(context.Context, string) (api.OutboundAIPrompt, error)
		DeleteOutboundAIPrompt(context.Context, string) error
		SetOutboundAIPromptCanary(context.Context, string, api.PutOutboundAIPromptCanaryRequest) (api.OutboundAIPromptCanary, error)
		PromoteOutboundAIPromptCanary(context.Context, string) (api.OutboundAIPrompt, error)
		DeleteOutboundAIPromptCanary(context.Context, string) error
		GetOutboundAIPromptStats(context.Context, string) (api.OutboundAIPromptStats, error)
		CreateOutboundAIPromptEvaluation(context.Context, string, api.CreateOutboundAIPromptEvaluationRequest) (api.OutboundAIPromptEvaluation, error)
		ListOutboundAIPromptEvaluations(context.Context, string) (api.OutboundAIPromptEvaluationList, error)
		GetOutboundAIPromptEvaluation(context.Context, string) (api.OutboundAIPromptEvaluation, error)
	}
	ctx context.Context
}

func promptUsageErr(action string) int {
	return printErr("Invalid ai prompts "+action+" arguments", errors.New(aiPromptsUsage))
}

func optionalVersion(version *int) string {
	if version == nil {
		return "-"
	}
	return strconv.Itoa(*version)
}

func (c aiPromptsCLI) list() int {
	prompts, err := c.client.ListOutboundAIPrompts(c.ctx)
	if err != nil {
		return printErr("Could not list AI prompts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(prompts))
	}
	if len(prompts.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no AI prompts)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tLIVE\tLATEST\tCANARY\tUPDATED\tDESCRIPTION")
	for _, prompt := range prompts.Items {
		canary := "-"
		if prompt.Canary != nil {
			canary = fmt.Sprintf("v%d %d%% %s", prompt.Canary.Version, prompt.Canary.Percent, prompt.Canary.State)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", prompt.Name, optionalVersion(prompt.LiveVersion), prompt.LatestVersion, canary,
			prompt.UpdatedAt.UTC().Format(time.RFC3339), prompt.Description)
	}
	_ = tw.Flush()
	return 0
}

// get shows a prompt, its versions and the live (or chosen) version.
func (c aiPromptsCLI) get(args []string) int {
	fs := newFlagSet("ai prompts get", flag.ContinueOnError)
	versionFlag := fs.Int("version", 0, "show this version instead of the live one")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return promptUsageErr("get")
	}
	name := fs.Arg(0)
	prompt, err := c.client.GetOutboundAIPrompt(c.ctx, name)
	if err != nil {
		return printErr("Could not read the AI prompt", err)
	}
	number := *versionFlag
	if number == 0 && prompt.LiveVersion != nil {
		number = *prompt.LiveVersion
	}
	var version *api.OutboundAIPromptVersion
	if number > 0 {
		read, err := c.client.GetOutboundAIPromptVersion(c.ctx, name, number)
		if err != nil {
			return printErr("Could not read the prompt version", err)
		}
		version = &read
	}
	versions, err := c.client.ListOutboundAIPromptVersions(c.ctx, name)
	if err != nil {
		return printErr("Could not list prompt versions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"prompt": prompt, "version": version, "versions": versions.Items}))
	}
	_, _ = fmt.Fprintf(osStdout, "%s  live v%s, previous v%s, latest v%d\n  call: POST $GREGALE_OUTBOUND_URL/p/%s\n", prompt.Name,
		optionalVersion(prompt.LiveVersion), optionalVersion(prompt.PreviousLiveVersion), prompt.LatestVersion, prompt.Name)
	if prompt.Canary != nil {
		_, _ = fmt.Fprintf(osStdout, "  canary: v%d at %d%%, %s\n", prompt.Canary.Version, prompt.Canary.Percent, prompt.Canary.State)
	}
	if version != nil {
		_, _ = fmt.Fprintf(osStdout, "\nVersion %d:\n%s", version.Version, formatPromptVersion(*version))
	}
	_, _ = fmt.Fprintln(osStdout, "\nVersions:")
	for _, summary := range versions.Items {
		_, _ = fmt.Fprintf(osStdout, "  v%d  %s  %s  %s\n", summary.Version, summary.CreatedAt.UTC().Format(time.RFC3339),
			formatPromptTarget(summary.Target), summary.Note)
	}
	return 0
}

func formatPromptTarget(target api.OutboundAIPromptTarget) string {
	if target.RouteID != "" {
		if target.Model != "" {
			return "route " + target.RouteID + " (model " + target.Model + ")"
		}
		return "route " + target.RouteID
	}
	return target.IntegrationID + ":" + target.Model
}

// formatPromptVersion renders a version as lines that diff well.
func formatPromptVersion(version api.OutboundAIPromptVersion) string {
	var out strings.Builder
	_, _ = fmt.Fprintf(&out, "target: %s\n", formatPromptTarget(version.Target))
	if params, err := json.Marshal(version.Params); err == nil && string(params) != "{}" {
		_, _ = fmt.Fprintf(&out, "params: %s\n", params)
	}
	for _, key := range sortedKeys(version.Defaults) {
		_, _ = fmt.Fprintf(&out, "default %s: %s\n", key, version.Defaults[key])
	}
	for _, message := range version.Messages {
		_, _ = fmt.Fprintf(&out, "[%s]\n", message.Role)
		for _, line := range strings.Split(message.Content, "\n") {
			_, _ = fmt.Fprintf(&out, "  %s\n", line)
		}
	}
	if version.Note != "" {
		_, _ = fmt.Fprintf(&out, "note: %s\n", version.Note)
	}
	return out.String()
}

// promptFile is the file `push` reads, as YAML or JSON.
type promptFile struct {
	Name        string                        `json:"name"`
	Description *string                       `json:"description"`
	Messages    []api.OutboundAIPromptMessage `json:"messages"`
	Defaults    map[string]string             `json:"defaults"`
	Target      api.OutboundAIPromptTarget    `json:"target"`
	Params      api.OutboundAIPromptParams    `json:"params"`
	Note        string                        `json:"note"`
}

func readPromptFile(path string) (string, api.PushOutboundAIPromptVersionRequest, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the user names the file to push.
	if err != nil {
		return "", api.PushOutboundAIPromptVersionRequest{}, err
	}
	// YAML is a superset of JSON. Decode it generically and re-encode it as
	// JSON so the API types' json tags (integration_id, max_tokens) apply.
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return "", api.PushOutboundAIPromptVersionRequest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var file promptFile
	encoded, err := json.Marshal(generic)
	if err == nil {
		err = json.Unmarshal(encoded, &file)
	}
	if err != nil {
		return "", api.PushOutboundAIPromptVersionRequest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return file.Name, api.PushOutboundAIPromptVersionRequest{Description: file.Description, Messages: file.Messages,
		Defaults: file.Defaults, Target: file.Target, Params: file.Params, Note: file.Note}, nil
}

func (c aiPromptsCLI) push(args []string) int {
	fs := newFlagSet("ai prompts push", flag.ContinueOnError)
	live := fs.Bool("live", false, "make the pushed version live (a prompt's first version always is)")
	nameFlag := fs.String("name", "", "prompt name (default: the file's name field)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return promptUsageErr("push")
	}
	name, req, err := readPromptFile(fs.Arg(0))
	if err != nil {
		return printErr("Could not read the prompt file", err)
	}
	if *nameFlag != "" {
		name = *nameFlag
	}
	req.Live = *live
	if !api.ValidOutboundAIPromptName(name) {
		return printErr("Invalid prompt", errors.New("the prompt needs a DNS-label name (the file's name field or --name)"))
	}
	if problem := req.Problem(); problem != "" {
		return printErr("Invalid prompt", errors.New(problem))
	}
	pushed, err := c.client.PushOutboundAIPromptVersion(c.ctx, name, req)
	if err != nil {
		return printErr("Could not push the prompt", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(pushed))
	}
	switch {
	case !pushed.Created:
		_, _ = fmt.Fprintf(osStdout, "%s is unchanged at v%d (live v%s)\n", name, pushed.Version.Version, optionalVersion(pushed.Prompt.LiveVersion))
	case pushed.Prompt.LiveVersion != nil && *pushed.Prompt.LiveVersion == pushed.Version.Version:
		_, _ = fmt.Fprintf(osStdout, "%s v%d pushed and live\n", name, pushed.Version.Version)
	default:
		_, _ = fmt.Fprintf(osStdout, "%s v%d pushed; live is still v%s (gregale ai prompts promote %s %d, or canary it)\n", name,
			pushed.Version.Version, optionalVersion(pushed.Prompt.LiveVersion), name, pushed.Version.Version)
	}
	return 0
}

func positiveInt(raw string) (int, bool) {
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 1
}

func (c aiPromptsCLI) diff(args []string) int {
	if len(args) != 3 {
		return promptUsageErr("diff")
	}
	from, okFrom := positiveInt(args[1])
	to, okTo := positiveInt(args[2])
	if !okFrom || !okTo {
		return promptUsageErr("diff")
	}
	before, err := c.client.GetOutboundAIPromptVersion(c.ctx, args[0], from)
	if err != nil {
		return printErr("Could not read the prompt version", err)
	}
	after, err := c.client.GetOutboundAIPromptVersion(c.ctx, args[0], to)
	if err != nil {
		return printErr("Could not read the prompt version", err)
	}
	_, _ = fmt.Fprintf(osStdout, "--- %s v%d\n+++ %s v%d\n", args[0], from, args[0], to)
	for _, line := range diffLines(strings.Split(formatPromptVersion(before), "\n"), strings.Split(formatPromptVersion(after), "\n")) {
		_, _ = fmt.Fprintln(osStdout, line)
	}
	return 0
}

// diffLines is a line diff by longest common subsequence: unchanged lines
// start with a space, removed ones with -, added ones with +.
func diffLines(a, b []string) []string {
	common := make([][]int, len(a)+1)
	for i := range common {
		common[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, " "+a[i])
			i, j = i+1, j+1
		case common[i+1][j] >= common[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < len(b); j++ {
		out = append(out, "+"+b[j])
	}
	return out
}

// render shows the chat completions request a version renders for the
// given variables, without calling a model.
func (c aiPromptsCLI) render(args []string) int {
	fs := newFlagSet("ai prompts render", flag.ContinueOnError)
	versionFlag := fs.Int("version", 0, "render this version instead of the live one")
	var vars stringListFlag
	fs.Var(&vars, "var", "a variable as key=value (repeatable)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return promptUsageErr("render")
	}
	variables := map[string]string{}
	for _, pair := range vars {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return printErr("Invalid variable", fmt.Errorf("%q is not key=value", pair))
		}
		variables[key] = value
	}
	number := *versionFlag
	if number == 0 {
		prompt, err := c.client.GetOutboundAIPrompt(c.ctx, fs.Arg(0))
		if err != nil {
			return printErr("Could not read the AI prompt", err)
		}
		if prompt.LiveVersion == nil {
			return printErr("No live version", errors.New("push a version first"))
		}
		number = *prompt.LiveVersion
	}
	version, err := c.client.GetOutboundAIPromptVersion(c.ctx, fs.Arg(0), number)
	if err != nil {
		return printErr("Could not read the prompt version", err)
	}
	messages, missing := api.RenderOutboundAIPrompt(version.Messages, version.Defaults, variables)
	if missing != "" {
		return printErr("Missing variable", fmt.Errorf("set --var %s=... (the version has no default for it)", missing))
	}
	return jsonOut(writeJSON(map[string]any{"version": version.Version, "target": version.Target, "messages": messages,
		"params": version.Params}))
}

func (c aiPromptsCLI) promote(args []string) int {
	if len(args) != 2 {
		return promptUsageErr("promote")
	}
	version, ok := positiveInt(args[1])
	if !ok {
		return promptUsageErr("promote")
	}
	prompt, err := c.client.SetOutboundAIPromptLive(c.ctx, args[0], version)
	if err != nil {
		return printErr("Could not make the version live", err)
	}
	return printPromptLive(prompt)
}

func (c aiPromptsCLI) rollback(args []string) int {
	if len(args) != 1 {
		return promptUsageErr("rollback")
	}
	prompt, err := c.client.RollBackOutboundAIPrompt(c.ctx, args[0])
	if err != nil {
		return printErr("Could not roll the prompt back", err)
	}
	return printPromptLive(prompt)
}

func printPromptLive(prompt api.OutboundAIPrompt) int {
	if jsonOutput {
		return jsonOut(writeJSON(prompt))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: live v%s (previous v%s)\n", prompt.Name, optionalVersion(prompt.LiveVersion),
		optionalVersion(prompt.PreviousLiveVersion))
	return 0
}

func (c aiPromptsCLI) canary(args []string) int {
	if len(args) == 2 && (args[1] == "promote" || args[1] == "stop") {
		if args[1] == "stop" {
			if err := c.client.DeleteOutboundAIPromptCanary(c.ctx, args[0]); err != nil {
				return printErr("Could not stop the canary", err)
			}
			_, _ = fmt.Fprintf(osStdout, "%s canary stopped\n", args[0])
			return 0
		}
		prompt, err := c.client.PromoteOutboundAIPromptCanary(c.ctx, args[0])
		if err != nil {
			return printErr("Could not promote the canary", err)
		}
		return printPromptLive(prompt)
	}
	fs := newFlagSet("ai prompts canary", flag.ContinueOnError)
	percent := fs.Int("percent", 0, "share of unpinned calls the version serves, 1 to 50")
	canaryFlags := addPromptCanaryFlags(fs)
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 2 {
		return promptUsageErr("canary")
	}
	version, ok := positiveInt(fs.Arg(1))
	if !ok {
		return promptUsageErr("canary")
	}
	req := canaryFlags.request(version, *percent)
	if !req.Valid() {
		return printErr("Invalid canary", errors.New("a canary needs --percent from 1 to 50 and thresholds in range"))
	}
	canary, err := c.client.SetOutboundAIPromptCanary(c.ctx, fs.Arg(0), req)
	if err != nil {
		return printErr("Could not start the canary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(canary))
	}
	_, _ = fmt.Fprintf(osStdout, "%s canary: v%d serves %d%% of unpinned calls; rolled back on %d error-rate points worse after %d calls\n",
		fs.Arg(0), canary.Version, canary.Percent, canary.MaxErrorRateIncrease, canary.MinCalls)
	return 0
}

// promptCanaryFlags are a canary's optional thresholds.
type promptCanaryFlags struct {
	maxErrorIncrease, maxLatencyIncrease, minCalls, window *int
}

func addPromptCanaryFlags(fs *flag.FlagSet) promptCanaryFlags {
	return promptCanaryFlags{
		maxErrorIncrease:   fs.Int("max-error-increase", -1, "roll back past this many error-rate points worse (default 5)"),
		maxLatencyIncrease: fs.Int("max-latency-increase", 0, "roll back past this many percent slower time to first byte (default: off)"),
		minCalls:           fs.Int("min-calls", 0, "calls needed before judging (default 50)"),
		window:             fs.Int("window", 0, "minutes of calls to judge (default 30)"),
	}
}

func (f promptCanaryFlags) request(version, percent int) api.PutOutboundAIPromptCanaryRequest {
	req := api.PutOutboundAIPromptCanaryRequest{Version: version, Percent: percent}
	if *f.maxErrorIncrease >= 0 {
		req.MaxErrorRateIncrease = f.maxErrorIncrease
	}
	if *f.maxLatencyIncrease > 0 {
		req.MaxLatencyIncreasePercent = f.maxLatencyIncrease
	}
	if *f.minCalls > 0 {
		req.MinCalls = f.minCalls
	}
	if *f.window > 0 {
		req.WindowMinutes = f.window
	}
	return req.Normalized()
}

func (c aiPromptsCLI) stats(args []string) int {
	if len(args) != 1 {
		return promptUsageErr("stats")
	}
	stats, err := c.client.GetOutboundAIPromptStats(c.ctx, args[0])
	if err != nil {
		return printErr("Could not read prompt stats", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(stats))
	}
	_, _ = fmt.Fprintf(osStdout, "%s since %s (live v%s, canary v%s)\n", args[0], stats.WindowStart.UTC().Format(time.RFC3339),
		optionalVersion(stats.LiveVersion), optionalVersion(stats.CanaryVersion))
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "VERSION\tCALLS\tERRORS\tMEAN TTFB MS\tCOST\tGOOD\tBAD")
	for _, version := range stats.Versions {
		_, _ = fmt.Fprintf(tw, "v%d\t%d\t%d\t%s\t%s\t%d\t%d\n", version.Version, version.Calls, version.Errors,
			formatOptionalCount(version.MeanTTFBMS), financialMoney(version.CostMillicents), version.GoodFeedback, version.BadFeedback)
	}
	_ = tw.Flush()
	return 0
}

// readPromptCases reads variable sets: a JSON array of objects, or one JSON
// object per line.
func readPromptCases(path string) ([]map[string]string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the user names the cases file.
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	var cases []map[string]string
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := json.Unmarshal(trimmed, &cases); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		return cases, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for line := 1; scanner.Scan(); line++ {
		if text := strings.TrimSpace(scanner.Text()); text != "" {
			var variables map[string]string
			if err := json.Unmarshal([]byte(text), &variables); err != nil {
				return nil, fmt.Errorf("%s line %d: %w", path, line, err)
			}
			cases = append(cases, variables)
		}
	}
	return cases, scanner.Err()
}

func (c aiPromptsCLI) evaluate(args []string) int {
	if len(args) == 2 && (args[0] == "show" || args[0] == "list") {
		return c.evaluateRead(args[0], args[1])
	}
	fs := newFlagSet("ai prompts evaluate", flag.ContinueOnError)
	casesPath := fs.String("cases", "", "a JSON array, or JSON lines, of variable sets (1 to 50)")
	judge := fs.String("judge", "", "the AI integration whose model judges the answers")
	judgeModel := fs.String("judge-model", "", "the judge model")
	maxLossRate := fs.Int("max-loss-rate", api.OutboundAIEvaluationMaxLossRateDefault, "pass when losses and errors are at most this percent of the cases")
	canaryPercent := fs.Int("canary-percent", 0, "start a canary with this share of calls when the evaluation passes (0: none)")
	noWait := fs.Bool("no-wait", false, "print the evaluation without waiting for its verdict")
	timeout := fs.Duration("timeout", 30*time.Minute, "how long to wait for the verdict")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 2 || *casesPath == "" {
		return promptUsageErr("evaluate")
	}
	version, ok := positiveInt(fs.Arg(1))
	if !ok {
		return promptUsageErr("evaluate")
	}
	cases, err := readPromptCases(*casesPath)
	if err != nil {
		return printErr("Could not read the cases", err)
	}
	req := api.CreateOutboundAIPromptEvaluationRequest{Version: version, Cases: cases, JudgeIntegrationID: *judge,
		JudgeModel: *judgeModel, MaxLossRate: maxLossRate}
	if *canaryPercent > 0 {
		req.Canary = &api.PutOutboundAIPromptCanaryRequest{Version: version, Percent: *canaryPercent}
	}
	if problem := req.Problem(); problem != "" {
		return printErr("Invalid evaluation", errors.New(problem))
	}
	evaluation, err := c.client.CreateOutboundAIPromptEvaluation(c.ctx, fs.Arg(0), req)
	if err != nil {
		return printErr("Could not start the evaluation", err)
	}
	deadline := time.Now().Add(*timeout)
	for !*noWait && (evaluation.State == api.OutboundAIEvaluationPending || evaluation.State == api.OutboundAIEvaluationRunning) {
		if time.Now().After(deadline) {
			return printErr("Evaluation still running", fmt.Errorf("check later with gregale ai prompts evaluate show %s", evaluation.ID))
		}
		time.Sleep(2 * time.Second)
		if evaluation, err = c.client.GetOutboundAIPromptEvaluation(c.ctx, evaluation.ID); err != nil {
			return printErr("Could not read the evaluation", err)
		}
	}
	return printPromptEvaluation(evaluation)
}

func (c aiPromptsCLI) evaluateRead(action, id string) int {
	if action == "show" {
		evaluation, err := c.client.GetOutboundAIPromptEvaluation(c.ctx, id)
		if err != nil {
			return printErr("Could not read the evaluation", err)
		}
		return printPromptEvaluation(evaluation)
	}
	list, err := c.client.ListOutboundAIPromptEvaluations(c.ctx, id)
	if err != nil {
		return printErr("Could not list evaluations", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no evaluations)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tVERSIONS\tSTATE\tVERDICT\tWINS\tTIES\tLOSSES\tERRORS\tCREATED")
	for _, e := range list.Items {
		_, _ = fmt.Fprintf(tw, "%s\tv%d vs v%d\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n", e.ID, e.CandidateVersion, e.BaselineVersion, e.State,
			promptEvaluationOutcome(e), e.Wins, e.Ties, e.Losses, e.CandidateErrors, e.CreatedAt.UTC().Format(time.RFC3339))
	}
	_ = tw.Flush()
	return 0
}

func promptEvaluationOutcome(e api.OutboundAIPromptEvaluation) string {
	switch {
	case e.Verdict != nil:
		return *e.Verdict
	case e.ErrorCode != nil:
		return *e.ErrorCode
	}
	return "-"
}

func printPromptEvaluation(e api.OutboundAIPromptEvaluation) int {
	if jsonOutput {
		return jsonOut(writeJSON(e))
	}
	_, _ = fmt.Fprintf(osStdout, "Evaluation %s of %s v%d against v%d: %s, %s\n", e.ID, e.Prompt, e.CandidateVersion, e.BaselineVersion,
		e.State, promptEvaluationOutcome(e))
	_, _ = fmt.Fprintf(osStdout, "  %d cases: %d wins, %d ties, %d losses, %d candidate errors, %d judge errors (pass at %d%% losses and errors)\n",
		e.Cases, e.Wins, e.Ties, e.Losses, e.CandidateErrors, e.JudgeErrors, e.MaxLossRate)
	_, _ = fmt.Fprintf(osStdout, "  baseline: cost %s, mean TTFB %s; candidate: cost %s, mean TTFB %s\n",
		financialMoney(e.Baseline.CostMillicents), formatOptionalCount(e.Baseline.MeanTTFBMS),
		financialMoney(e.Candidate.CostMillicents), formatOptionalCount(e.Candidate.MeanTTFBMS))
	if e.Canary != nil {
		started := "not started"
		if e.CanaryStarted {
			started = "started"
		}
		_, _ = fmt.Fprintf(osStdout, "  canary at %d%%: %s\n", e.Canary.Percent, started)
	}
	for _, item := range e.Items {
		_, _ = fmt.Fprintf(osStdout, "  case %d: %s\n", item.Case+1, item.Verdict)
	}
	if e.ContentWithheld {
		_, _ = fmt.Fprintln(osStdout, "  (answer texts withheld: reading them needs admin or secrets:read)")
	}
	return 0
}

func (c aiPromptsCLI) remove(args []string) int {
	if len(args) != 1 {
		return promptUsageErr("rm")
	}
	if err := c.client.DeleteOutboundAIPrompt(c.ctx, args[0]); err != nil {
		return printErr("Could not delete the AI prompt", err)
	}
	_, _ = fmt.Fprintf(osStdout, "AI prompt %s deleted\n", args[0])
	return 0
}
