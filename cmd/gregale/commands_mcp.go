package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"golang.org/x/term"
)

func cmdMCP(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale mcp init|deploy|policy|doctor|tools|resources|resource-read|resource-watch|prompts|prompt-get|complete|call|task-get|task-wait|task-cancel|watch|config|lock|diff [flags]", "mcp")
		return 1
	}
	switch args[0] {
	case "init":
		return cmdMCPInit(args[1:])
	case "deploy":
		return cmdMCPDeploy(args[1:])
	case "policy":
		return cmdMCPPolicy(args[1:])
	case "lock":
		return cmdMCPLock(args[1:])
	case "diff":
		return cmdMCPDiff(args[1:])
	case "watch":
		return cmdMCPWatch(args[1:])
	case "resource-watch":
		return cmdMCPResourceWatch(args[1:])
	case "complete":
		return cmdMCPComplete(args[1:])
	case "doctor", "tools", "resources", "resource-read", "prompts", "prompt-get", "call", "task-get", "task-wait", "task-cancel", "config":
		return cmdMCPRemote(args[0], args[1:])
	default:
		return printErr("Unknown MCP command", fmt.Errorf("%q", args[0]))
	}
}

func cmdMCPInit(args []string) int {
	fs := newFlagSet("mcp-init", flag.ContinueOnError)
	dir := fs.String("path", "", "empty destination directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *dir == "" {
		return printErr("MCP init", errors.New("--path is required"))
	}
	return cmdInit([]string{"--template", "mcp-node", "--path", *dir})
}

func mcpToken(env string) (string, error) {
	if env == "" {
		return "", nil
	}
	token, ok := os.LookupEnv(env)
	if !ok || token == "" {
		return "", fmt.Errorf("client token environment variable %q is empty", env)
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", errors.New("MCP client token contains a newline")
	}
	return token, nil
}

func mcpArguments(inline, file string) (map[string]any, error) {
	if inline != "" && file != "" {
		return nil, errors.New("--arguments and --arguments-file are mutually exclusive")
	}
	var r io.Reader = bytes.NewBufferString(inline)
	if file != "" {
		f, err := openCustomerFile(file)
		if err != nil {
			return nil, fmt.Errorf("read MCP arguments: %w", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	} else if inline == "" {
		return map[string]any{}, nil
	}
	body, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read MCP arguments: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, errors.New("MCP arguments exceed 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var args map[string]any
	if err := d.Decode(&args); err != nil {
		return nil, fmt.Errorf("MCP arguments must be a JSON object: %w", err)
	}
	if args == nil {
		return nil, errors.New("MCP arguments must be a JSON object")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("MCP arguments must contain one JSON object")
	}
	return args, nil
}

func mcpInputIsTerminal(input io.Reader) bool {
	f, ok := input.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func mcpInputResponses(path string) (map[string]mcphosting.InputResponse, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, fmt.Errorf("open input response file: %w", err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read input response file: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, errors.New("input response file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var responses map[string]mcphosting.InputResponse
	if err := decoder.Decode(&responses); err != nil || responses == nil {
		return nil, errors.New("input response file must be one JSON object keyed by elicitation request ID")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("input response file must contain one JSON object")
	}
	if len(responses) == 0 {
		return nil, errors.New("input response file must contain at least one request response")
	}
	for id, response := range responses {
		if id == "" || len(id) > 256 {
			return nil, errors.New("input response file contains an invalid request ID")
		}
		switch response.Action {
		case "accept":
			if response.Content == nil {
				return nil, fmt.Errorf("input response %q must include an object content", id)
			}
		case "decline", "cancel":
			if response.Content != nil {
				return nil, fmt.Errorf("declined input response %q must not include content", id)
			}
		default:
			return nil, fmt.Errorf("input response %q must use accept, decline or cancel", id)
		}
	}
	return responses, nil
}

func promptMCPInput(scanner *bufio.Scanner, output io.Writer, form mcphosting.InputRequest) (mcphosting.InputResponse, error) {
	schema, err := json.MarshalIndent(form.Schema, "", "  ")
	if err != nil || len(schema) > 64<<10 {
		return mcphosting.InputResponse{}, errors.New("MCP server supplied an invalid input form")
	}
	_, _ = fmt.Fprintf(output, "\nMCP server requests additional input for tool %q (server-provided text is untrusted):\n", form.Tool)
	if form.Message != "" {
		_, _ = fmt.Fprintf(output, "%s\n", mcpSafeTerminalText(form.Message))
	}
	_, _ = fmt.Fprintf(output, "Form schema:\n%s\n", schema)
	_, _ = fmt.Fprintln(output, `Enter a JSON object to accept, or type "decline" or "cancel".`)
	for attempt := 0; attempt < 3; attempt++ {
		_, _ = fmt.Fprint(output, "Input: ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return mcphosting.InputResponse{}, fmt.Errorf("read interactive input: %w", err)
			}
			return mcphosting.InputResponse{Action: "cancel"}, nil
		}
		line := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(line) {
		case "decline":
			return mcphosting.InputResponse{Action: "decline"}, nil
		case "cancel":
			return mcphosting.InputResponse{Action: "cancel"}, nil
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		var content map[string]any
		if err := decoder.Decode(&content); err == nil && content != nil {
			var extra any
			if decoder.Decode(&extra) == io.EOF {
				return mcphosting.InputResponse{Action: "accept", Content: content}, nil
			}
		}
		_, _ = fmt.Fprintln(output, "Enter one JSON object, decline, or cancel.")
	}
	return mcphosting.InputResponse{}, errors.New("interactive input must be one JSON object; no response was sent")
}

func mcpSafeTerminalText(value string) string {
	const (
		plain = iota
		escape
		csi
		stringSequence
		stringEscape
	)
	state := plain
	var safe strings.Builder
	for _, r := range value {
		switch state {
		case plain:
			switch r {
			case '\x1b':
				state = escape
				continue
			case '\u009b':
				state = csi
				continue
			}
			if r != '\n' && r != '\t' && unicode.IsControl(r) {
				continue
			}
			safe.WriteRune(r)
		case escape:
			switch r {
			case '[':
				state = csi
			case ']':
				state = stringSequence
			case 'P', '^', '_':
				state = stringSequence
			default:
				state = plain
			}
		case csi:
			if r >= 0x40 && r <= 0x7e {
				state = plain
			}
		case stringSequence:
			switch r {
			case '\a':
				state = plain
			case '\x1b':
				state = stringEscape
			}
		case stringEscape:
			switch r {
			case '\\', '\a':
				state = plain
			case '\x1b':
			default:
				state = stringSequence
			}
		}
	}
	return safe.String()
}

func mcpEndpoint(ctx context.Context, endpoint, app, path string) (string, error) {
	if endpoint != "" && app != "" {
		return "", errors.New("choose --url or --app")
	}
	if endpoint != "" {
		return endpoint, nil
	}
	if app == "" {
		return "", errors.New("--url or --app is required")
	}
	c, err := authedClient()
	if err != nil {
		return "", err
	}
	metadata, err := c.GetApp(ctx, app)
	if err != nil {
		return "", err
	}
	return (mcphosting.Config{Endpoint: path}).URL(canonicalAppURL(metadata))
}

func cmdMCPRemote(command string, args []string) int {
	fs := newFlagSet("mcp-"+command, flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug; resolves its public URL")
	path := fs.String("endpoint", "/mcp", "endpoint path when using --app")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP client access token")
	legacy := fs.Bool("legacy", false, "also check legacy compatibility (doctor), or use protocol 2025-11-25")
	interactive := fs.Bool("interactive", false, "answer modern MCP input forms in the terminal (call only)")
	inputResponsesFile := fs.String("input-responses-file", "", "JSON file with elicitation responses keyed by request ID (call only)")
	enableTasks := fs.Bool("tasks", false, "accept modern MCP task handles for later inspection (call only)")
	waitForTask := fs.Bool("wait", false, "wait for a task result and request cancellation if this command times out (call only; implies --tasks)")
	tool := fs.String("tool", "", "tool to call")
	streamTool := fs.String("stream-tool", "", "explicitly execute this tool to verify live progress (doctor)")
	inline := fs.String("arguments", "", "tool arguments as a JSON object")
	file := fs.String("arguments-file", "", "read tool arguments from a JSON file")
	name := fs.String("name", "gregale", "connection name (config)")
	uri := fs.String("uri", "", "resource URI to read (resource-read only)")
	promptName := fs.String("prompt", "", "prompt name to render (prompt-get only)")
	taskID := fs.String("task-id", "", "opaque task ID (task-get or task-cancel)")
	timeout := fs.Duration("timeout", 30*time.Second, "total diagnostic timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		return printErr("Invalid MCP flags", errors.New("unexpected positional arguments or nonpositive timeout"))
	}
	if *interactive && *inputResponsesFile != "" {
		return printErr("Invalid MCP input flags", errors.New("choose either --interactive or --input-responses-file"))
	}
	inputMode := *interactive || *inputResponsesFile != ""
	if inputMode && ((command != "call" && command != "task-wait") || *legacy) {
		return printErr("Invalid MCP input flags", errors.New("interactive input is supported only for modern mcp call and mcp task-wait"))
	}
	if (*enableTasks || *waitForTask) && (command != "call" || *legacy) {
		return printErr("Invalid MCP Tasks flags", errors.New("MCP Tasks support is available only for modern mcp call"))
	}
	if *taskID != "" && command != "task-get" && command != "task-wait" && command != "task-cancel" {
		return printErr("Invalid MCP task flags", errors.New("--task-id is supported only by mcp task-get, mcp task-wait and mcp task-cancel"))
	}
	if (command == "task-get" || command == "task-wait" || command == "task-cancel") && (*taskID == "" || *legacy) {
		return printErr("Invalid MCP task flags", errors.New("modern task-get, task-wait and task-cancel require --task-id"))
	}
	if *interactive && !mcpInputIsTerminal(osStdin) {
		return printErr("MCP interactive input", errors.New("--interactive requires terminal stdin; use --input-responses-file for non-interactive calls"))
	}
	responses := map[string]mcphosting.InputResponse(nil)
	if *inputResponsesFile != "" {
		var err error
		responses, err = mcpInputResponses(*inputResponsesFile)
		if err != nil {
			return printErr("MCP input responses", err)
		}
	}
	timeoutValue := *timeout
	timeoutExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			timeoutExplicit = true
		}
	})
	if (*interactive || *waitForTask || command == "task-wait") && !timeoutExplicit {
		timeoutValue = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeoutValue)
	defer cancel()
	resolved, err := mcpEndpoint(ctx, *endpoint, *app, *path)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	version := mcphosting.ProtocolVersion
	if *legacy && command != "doctor" {
		version = mcphosting.LegacyProtocolVersion
	}
	c, err := mcphosting.NewClient(resolved, token, version)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	c.HTTP.Timeout = timeoutValue
	arguments, err := mcpArguments(*inline, *file)
	if err != nil {
		return printErr("MCP arguments", err)
	}
	var responder mcphosting.InputResponder
	if *interactive {
		scanner := bufio.NewScanner(osStdin)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		responder = func(_ context.Context, form mcphosting.InputRequest) (mcphosting.InputResponse, error) {
			return promptMCPInput(scanner, osStderr, form)
		}
	} else if responses != nil {
		used := make(map[string]bool, len(responses))
		responder = func(_ context.Context, form mcphosting.InputRequest) (mcphosting.InputResponse, error) {
			response, ok := responses[form.ID]
			if !ok {
				return mcphosting.InputResponse{}, fmt.Errorf("input response file has no entry for request %q", form.ID)
			}
			if used[form.ID] {
				return mcphosting.InputResponse{}, fmt.Errorf("server requested form %q again; interactive retry is required", form.ID)
			}
			used[form.ID] = true
			return response, nil
		}
	}
	if command == "resource-read" && *uri == "" {
		return printErr("MCP resource read", errors.New("--uri is required"))
	}
	if command == "prompt-get" && *promptName == "" {
		return printErr("MCP prompt get", errors.New("--prompt is required"))
	}
	return runMCPRemoteWithOptions(ctx, command, c, *legacy, *tool, *streamTool, *name, *uri, *promptName, *taskID, arguments, responder, *enableTasks || *waitForTask, *waitForTask)
}

func runMCPRemoteWithOptions(ctx context.Context, command string, c *mcphosting.Client, legacy bool, tool, streamTool, name, uri, promptName, taskID string, args map[string]any, respond mcphosting.InputResponder, enableTasks, waitForTask bool) int {
	if command == "config" {
		return jsonOut(writeJSON(mcphosting.ConnectionConfig(name, c.Endpoint)))
	}
	if command == "doctor" {
		report := mcphosting.Doctor(ctx, c, legacy, streamTool, args)
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
		if !report.OK {
			return 1
		}
		return 0
	}
	if err := c.Initialize(ctx); err != nil {
		return printErr("MCP initialize", err)
	}
	switch command {
	case "resources":
		// A server that does not implement a list method offers none of that
		// kind: report an empty catalog rather than an error (production-us
		// hunt #4: a tools-only server answered "MCP endpoint returned HTTP 404").
		resources, err := c.Resources(ctx)
		if err != nil && !mcphosting.IsMethodNotFound(err) {
			return printErr("MCP resource discovery", err)
		}
		templates, err := c.ResourceTemplates(ctx)
		if err != nil && !mcphosting.IsMethodNotFound(err) {
			return printErr("MCP resource template discovery", err)
		}
		if resources == nil {
			resources = []mcphosting.Resource{}
		}
		if templates == nil {
			templates = []mcphosting.ResourceTemplate{}
		}
		return jsonOut(writeJSON(map[string]any{"resources": resources, "resource_templates": templates}))
	case "resource-read":
		x, err := c.ReadResource(ctx, uri)
		if err != nil {
			return printErr("MCP resource read", err)
		}
		return jsonOut(writeJSON(x))
	case "prompts":
		prompts, _, err := c.Prompts(ctx)
		if err != nil && !mcphosting.IsMethodNotFound(err) {
			return printErr("MCP prompt discovery", err)
		}
		if prompts == nil {
			prompts = []mcphosting.Prompt{}
		}
		return jsonOut(writeJSON(map[string]any{"prompts": prompts}))
	case "prompt-get":
		promptArgs := make(map[string]string, len(args))
		for key, value := range args {
			text, ok := value.(string)
			if !ok {
				return printErr("MCP prompt arguments", fmt.Errorf("argument %q must be a string", key))
			}
			promptArgs[key] = text
		}
		x, err := c.GetPrompt(ctx, promptName, promptArgs)
		if err != nil {
			return printErr("MCP prompt get", err)
		}
		return jsonOut(writeJSON(x))
	case "task-get":
		task, _, err := c.GetTask(ctx, taskID)
		if err != nil {
			return printErr("MCP task get", err)
		}
		return jsonOut(writeJSON(task))
	case "task-wait":
		task, err := c.WaitTask(ctx, taskID, mcphosting.CallOptions{
			Responder: respond,
			OnTask: func(task mcphosting.Task) {
				_, _ = fmt.Fprintf(osStderr, "MCP task status: %s\n", task.Status)
			},
		})
		if task.TaskID != "" {
			if code := jsonOut(writeJSON(task)); code != 0 {
				return code
			}
		}
		if err != nil {
			return printErr("MCP task wait", err)
		}
		return 0
	case "task-cancel":
		if _, err := c.CancelTask(ctx, taskID); err != nil {
			return printErr("MCP task cancel", err)
		}
		return jsonOut(writeJSON(map[string]any{"task_id": taskID, "cancellation_requested": true}))
	}
	tools, discovery, err := c.Tools(ctx)
	if err != nil {
		return printErr("MCP tool discovery", err)
	}
	if command == "tools" {
		result := map[string]any{"tools": tools}
		if len(discovery.RejectedTools) != 0 {
			result["rejected_tools"] = discovery.RejectedTools
		}
		return jsonOut(writeJSON(result))
	}
	if tool == "" {
		return printErr("MCP call", errors.New("--tool is required"))
	}
	for _, candidate := range tools {
		if candidate.Name != tool {
			continue
		}
		var x mcphosting.Exchange
		x, err = c.CallWithOptions(ctx, candidate, args, mcphosting.CallOptions{
			Progress: true, Responder: respond, EnableTasks: enableTasks, WaitForTask: waitForTask,
			OnTask: func(task mcphosting.Task) {
				_, _ = fmt.Fprintf(osStderr, "MCP task status: %s\n", task.Status)
			},
		})
		if x.Result != nil {
			if code := jsonOut(writeJSON(x)); code != 0 {
				return code
			}
		}
		if err != nil {
			return printErr("MCP tool call", err)
		}
		return 0
	}
	return printErr("MCP call", fmt.Errorf("tool %q is not in the server's discovered contract", tool))
}

type mcpDeployOptions struct {
	path, slug, profile, tokenEnv, secretsFile string
	releasePolicy                              string
	timeout                                    int
}

func cmdMCPDeploy(args []string) int {
	fs := newFlagSet("mcp-deploy", flag.ContinueOnError)
	var o mcpDeployOptions
	fs.StringVar(&o.path, "path", ".", "source directory; uploads the current worktree")
	fs.StringVar(&o.slug, "name", "", "app slug")
	fs.StringVar(&o.profile, "profile", "", "app resource profile")
	fs.StringVar(&o.tokenEnv, "token-env", "", "MCP client token for post-deployment verification")
	fs.StringVar(&o.secretsFile, "secrets-file", "", "sealed app secrets configured before deployment")
	fs.StringVar(&o.releasePolicy, "release-policy", "", "role-specific catalog baselines to verify before promotion")
	fs.IntVar(&o.timeout, "timeout", 1200, "deployment wait timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || o.slug == "" || o.timeout <= 0 {
		return printErr("Invalid MCP deploy", errors.New("--name and a positive --timeout are required"))
	}
	return runMCPDeploy(o)
}

func runMCPDeploy(o mcpDeployOptions) int {
	dir, err := filepath.Abs(o.path)
	if err != nil {
		return printErr("MCP source", err)
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return printErr("MCP configuration", err)
	}
	token, err := mcpToken(o.tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	if cfg.Auth.Mode == "external-oauth" && token == "" {
		return printErr("MCP verification", errors.New("external-oauth deploy requires --token-env with a client access token for verification"))
	}
	roles, err := loadMCPReleasePolicy(o.releasePolicy)
	if err != nil {
		return printErr("MCP release policy", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	account, err := c.Whoami(ctx)
	if err != nil {
		return printErr("MCP plan check", err)
	}
	if !api.Plan(account.Plan).StreamingEnabled() {
		return printErr("MCP streaming unavailable", errors.New("the stateless MCP hosting profile requires a plan that supports streaming"))
	}
	if !jsonOutput {
		PrintProgress(osStdout, "Deploying stateless MCP (%s client access)", cfg.Auth.Mode)
	}
	deployArgs := []string{"--path", dir, "--source=worktree", "--name", o.slug, "--app", "--wait", "--no-traffic", "--timeout", strconv.Itoa(o.timeout)}
	if o.profile != "" {
		deployArgs = append(deployArgs, "--profile", o.profile)
	}
	if o.secretsFile != "" {
		deployArgs = append(deployArgs, "--secrets-file", o.secretsFile)
	}
	// Preserve the existing ingress gate until the new app passes readiness.
	var candidate api.DeploymentResponse
	code := quietMCPDeploy(deployArgs, deployExecution{onQueued: func(dep api.DeploymentResponse) { candidate = dep }})
	if code != 0 {
		return code
	}
	return finishVerifiedMCPDeploy(ctx, c, o.slug, candidate.ID, cfg, token, roles)
}

func quietMCPDeploy(args []string, execution deployExecution) int {
	old := osStdout
	osStdout = io.Discard
	defer func() { osStdout = old }()
	return cmdDeployTarballToExisting(context.Background(), args, false, execution)
}
