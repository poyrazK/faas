package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/safetext"
)

type startRunner struct {
	ctx            context.Context
	prompt         startPrompt
	secretsFile    string
	waitTimeout    time.Duration
	client         *Client
	account        api.AccountResponse
	session        startSession
	statePath      string
	startedAt      time.Time
	lastRequest    *startRequestResult
	requestFailure *startRequestFailure
	progress       *startProgress
	// Use the same deploy implementation as deploy/dev. The local seam lets
	// recovery tests observe acceptance without running a remote build.
	deploy func(context.Context, []string, bool, ...deployExecution) int
}

func cmdStart(args []string) (code int) {
	if len(args) != 0 {
		return printErr("Start takes no flags or arguments", errors.New("run gregale start and follow the prompts; use gregale deploy for deployment options"))
	}
	if jsonOutput {
		return printErr("Interactive session required", errors.New("gregale start does not support --json; use gregale deploy --json for automation"))
	}
	if !stdinIsTTY() {
		return printErr("Interactive session required", errors.New("run gregale start in a terminal; use gregale deploy for scripts"))
	}
	scope, err := filepath.Abs(".")
	if err != nil {
		return printErr("Could not resolve session directory", err)
	}
	statePath, err := startSessionPath(scope)
	if err != nil {
		return printErr("Could not locate session", err)
	}
	lock, err := lockStartSession(statePath)
	if err != nil {
		return printErr("Could not open session", err)
	}
	defer func() { _ = lock.Close() }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			code = 130
		}
	}()
	runner := startRunner{
		ctx: ctx, waitTimeout: defaultDeployWaitTimeout, statePath: statePath,
		prompt:    startPrompt{reader: bufio.NewReader(osStdin), writer: osStdout},
		deploy:    cmdDeployTarballToExisting,
		session:   startSession{ScopePath: scope, APIBase: apiBase()},
		startedAt: time.Now(),
	}
	_, _ = fmt.Fprintln(osStdout, "\nGregale · Let's get your app live")
	saved, found, err := loadStartSession(statePath)
	if err != nil {
		return printErr("Could not recover session", err)
	}
	if found && saved.APIBase != apiBase() {
		return printErr("Session belongs to another API", fmt.Errorf("restore the API configuration for %s before continuing", saved.APIBase))
	}
	if found && saved.ScopePath != scope {
		return printErr("Session directory does not match", errors.New("the saved session belongs to another directory"))
	}
	if code := runner.authenticate(); code != 0 {
		return code
	}
	if found && saved.AccountID != runner.account.ID {
		return printErr("Session belongs to another account", errors.New("log in to the account that created this session before continuing"))
	}
	resume := false
	if found && saved.DeploymentID != "" {
		choice, err := runner.prompt.choose(ctx, "Welcome back.", []string{"Continue " + saved.AppSlug, "Start a new launch"}, 0)
		if err != nil {
			return startInputExit(err)
		}
		resume = choice == 0
	}
	if resume {
		runner.session = saved
		if code := runner.resume(); code != 0 {
			if result, ready := runner.recoverFailure(code); !ready {
				return result
			}
		}
	} else {
		runner.session.AccountID = runner.account.ID
		if code := runner.selectSource(); code != 0 {
			return code
		}
		// An input EOF may close source selection successfully without a path.
		if runner.session.SourcePath == "" {
			return 0
		}
		if code, submitted := runner.reviewAndDeploy(); code != 0 {
			if result, ready := runner.recoverFailure(code); !ready {
				return result
			}
		} else if !submitted {
			return 0
		}
	}
	return runner.completeLaunch()
}

func (r *startRunner) authenticate() int {
	PrintProgress(osStdout, "1/4 · Connect your account")
	if loadToken() != "" {
		client, _ := authedClient()
		account, err := client.Whoami(r.ctx)
		if err == nil {
			return r.useAccount(client, account)
		}
		var problem *APIError
		if !errors.As(err, &problem) || problem.Problem.Status != http.StatusUnauthorized {
			return printErr("Could not verify account", err)
		}
		PrintWarn(osStdout, "Your saved login has expired.")
	}
	ok, err := r.prompt.confirm(r.ctx, "Open your browser to sign in")
	if err != nil {
		// Login is required: declining/EOF must stop the entire flow.
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return printErr("Login required", err)
	}
	if !ok {
		return printErr("Login required", errors.New("sign in with gregale login before starting a deployment"))
	}
	client := NewClient(apiBase(), "")
	code, err := client.MintCliAuthCode(r.ctx)
	if err != nil {
		return printErr("Could not start login", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Approve this session in your browser:\n  %s\n", code.URL)
	if err := openBrowser(code.URL); err != nil {
		PrintWarn(osStdout, "Open the link above manually: %v", err)
	}
	if result := waitForApproval(r.ctx, client, code); result != 0 {
		if r.ctx.Err() != nil {
			return 130
		}
		return result
	}
	client, err = authedClient()
	if err != nil {
		return printErr("Login required", err)
	}
	account, err := client.Whoami(r.ctx)
	if err != nil {
		return printErr("Could not verify account", err)
	}
	return r.useAccount(client, account)
}

func (r *startRunner) useAccount(client *Client, account api.AccountResponse) int {
	if account.ID == "" {
		return printErr("Could not verify account", errors.New("API returned an account without an ID"))
	}
	r.client, r.account = client, account
	PrintOK(osStdout, "%s · %s plan", account.Email, account.Plan)
	return 0
}

func startTemplateAllowed(name string) bool {
	return name == "hello-node" || name == "hello-python" || name == "hello-go"
}

func (r *startRunner) selectSource() int {
	PrintProgress(osStdout, "2/4 · Choose what to launch")
	path, template := "", ""
	choices := []string{"Deploy this directory", "Create a starter", "Choose another directory"}
	fallback := 0
	if detectShape(".") == shapeUnknown {
		fallback = 1
	}
	choice, err := r.prompt.choose(r.ctx, "What would you like to deploy?", choices, fallback)
	if err != nil {
		return startInputExit(err)
	}
	switch choice {
	case 0:
		path = "."
	case 1:
		n, err := r.prompt.choose(r.ctx, "Pick a starter. You keep a local copy to edit.", []string{"Node.js HTTP app", "Python HTTP app", "Go HTTP app"}, 0)
		if err != nil {
			return startInputExit(err)
		}
		template = []string{"hello-node", "hello-python", "hello-go"}[n]
	case 2:
		path, err = r.requiredLocalPath("Project directory")
		if err != nil {
			return startInputExit(err)
		}
	}
	if template != "" && path == "" {
		var err error
		path, err = r.prompt.text(r.ctx, "Starter directory", "./gregale-"+strings.TrimPrefix(template, "hello-"))
		if err != nil {
			return startInputExit(err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return printErr("Could not resolve source", err)
	}
	if template != "" {
		if err := checkDestEmpty(abs); err != nil {
			return printErr("Refusing to overwrite starter destination", err)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return printErr("Could not create starter directory", err)
		}
		if err := templates.Materialize(template, abs); err != nil {
			return printErr("Could not create starter", err)
		}
		PrintOK(osStdout, "Starter ready in %s", abs)
	}
	info, err := os.Stat(abs)
	if template == "" && err == nil && info.IsDir() {
		selected, err := r.selectProjectSource(abs)
		if err != nil {
			return startInputExit(err)
		}
		abs = selected
	}
	r.session.SourcePath, r.session.Template = abs, template
	r.session.AppSlug = ""
	return 0
}

func (r *startRunner) selectProjectSource(root string) (string, error) {
	scan, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		return "", fmt.Errorf("inspect project sources: %w", err)
	}
	var paths, labels []string
	seen := make(map[string]bool)
	for _, workload := range scan.Workloads {
		if workload.RootDir == "" || workload.RootDir == "." || (workload.Tier != reposcan.TierWorkspace && workload.Tier != reposcan.TierConvention) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(workload.RootDir))
		if !seen[path] && detectShape(path) != shapeUnknown {
			seen[path] = true
			paths = append(paths, path)
			labels = append(labels, workload.Name+" · "+workload.RootDir)
		}
	}
	if len(paths) == 0 {
		return root, nil
	}
	if detectShape(root) != shapeUnknown {
		paths, labels = append(paths, root), append(labels, "Deploy the root directory")
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	choice, err := r.prompt.choose(r.ctx, "I found project sources. Choose one service to launch.", labels, 0)
	if err != nil {
		return "", err
	}
	return paths[choice], nil
}

func (r *startRunner) reviewAndDeploy() (int, bool) {
	return r.reviewLaunch(nil)
}

func (r *startRunner) reviewLaunch(edit *startFileEdit) (int, bool) {
	PrintProgress(osStdout, "3/4 · Check source and review deployment")
	report, ready, err := r.sourcePreflight()
	if err != nil {
		return startInputExit(err), false
	}
	if !ready {
		return 0, false
	}
	r.session.HealthPath = ""
	if report.Profile != nil && safeStartHealthPath(report.Profile.HealthPath) {
		r.session.HealthPath = report.Profile.HealthPath
	}
	if r.session.AppSlug == "" {
		r.session.AppSlug = sanitizeProjectSlug(sourceNameForPath(r.session.SourcePath))
	}
	app, err := r.client.GetApp(r.ctx, r.session.AppSlug)
	exists := err == nil
	if err != nil {
		var problem *APIError
		if !errors.As(err, &problem) || problem.Problem.Status != http.StatusNotFound {
			return printErr("Could not check app name", err), false
		}
	}
	if edit != nil && (!exists || (r.session.AppID != "" && app.ID != r.session.AppID)) {
		return printErr("App identity changed", errors.New("the starter update must target the app recorded by this session")), false
	}
	accessLabel := "Public URL"
	if api.Plan(r.account.Plan).RequireAuthnDefault() {
		accessLabel = "API token required"
	}
	if exists {
		accessLabel = "Preserve existing access settings"
		if edit == nil {
			PrintWarn(osStdout, "App %s already exists. This will deploy a new revision to it.", r.session.AppSlug)
		}
	}
	action := "Create app and deploy"
	if exists {
		action = "Deploy new revision to existing app"
	}
	_, _ = fmt.Fprintf(osStdout, "\nReady to launch %s\n  Source   %s\n  Account  %s (%s plan)\n  Action   %s\n  Access   %s\n", r.session.AppSlug, r.session.SourcePath, r.account.Email, r.account.Plan, action, accessLabel)
	if exists {
		renderDeploymentAccess(osStdout, app, r.session.AppSlug)
	}
	if r.secretsFile != "" {
		_, _ = fmt.Fprintln(osStdout, "  Secrets  Seal values from the supplied secrets file before deployment")
	}
	_, _ = fmt.Fprintln(osStdout, "  Billing  Uses your current plan and normal deployment limits.")
	_, _ = fmt.Fprintln(osStdout, "  Files    Includes your uncommitted changes; normal source exclusions apply.")
	confirmation := "Deploy this app"
	if edit != nil {
		renderStartFileEdits([]startFileEdit{*edit})
		confirmation = "Apply this greeting and deploy a new revision"
	}
	ok, err := r.prompt.confirm(r.ctx, confirmation)
	if err != nil {
		return startInputExit(err), false
	}
	if !ok {
		PrintProgress(osStdout, "Nothing deployed. Your local files are ready when you are.")
		return 0, false
	}
	if edit != nil {
		if err := applyStartFileEdit(r.session.SourcePath, *edit); err != nil {
			return printErr("Could not apply the reviewed greeting", err), false
		}
		PrintOK(osStdout, "Updated %s", edit.path)
	}
	// Verify local persistence works before the first remote mutation. Keep
	// the last accepted ID during a redeploy until its replacement is accepted.
	if r.session.DeploymentID == "" {
		r.session.Status = "prepared"
	}
	if err := saveStartSession(r.statePath, r.session); err != nil {
		return printErr("Could not save deployment session", err), false
	}
	PrintProgress(osStdout, "4/4 · Build, boot and verify readiness")
	args := []string{"--path", r.session.SourcePath, "--source=worktree", "--name", r.session.AppSlug, "--timeout", strconv.Itoa(int(r.waitTimeout / time.Second))}
	if r.secretsFile != "" {
		args = append(args, "--secrets-file", r.secretsFile)
	}
	deployCtx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	var saveErr error
	defer func() {
		r.progress.Close()
		r.progress = nil
	}()
	execution := deployExecution{
		sourcePreflightChecked: true,
		compactProgress:        true,
		onWaitEnd: func() {
			r.progress.Close()
		},
		onStage: func(name, status string, duration int64, reason string) {
			if r.progress != nil {
				r.progress.observeStage(name, status, duration, reason)
			}
		},
		onQueued: func(dep api.DeploymentResponse) {
			r.session.DeploymentID, r.session.AppID, r.session.Status = dep.ID, dep.AppID, dep.Status
			r.session.Revision, r.lastRequest = dep.Revision, nil
			if dep.ID == "" {
				saveErr = errors.New("API accepted a deployment without an ID")
			} else {
				saveErr = saveStartSession(r.statePath, r.session)
			}
			if saveErr != nil {
				cancel()
				return
			}
			r.progress = newStartProgress(osStdout, Enabled(), "Waiting for a builder")
			r.progress.observeDeployment(dep)
		},
		onTerminal: r.terminal,
	}
	if r.secretsFile != "" {
		path, err := filepath.Abs(r.secretsFile)
		if err != nil {
			return printErr("Could not resolve secrets file", err), false
		}
		execution.extraSourceExcludes = append(execution.extraSourceExcludes, path)
	}
	code := r.deploy(deployCtx, args, false, execution)
	r.progress.Close()
	if saveErr != nil {
		PrintProgress(osStdout, "Inspect accepted deployment directly: gregale deployment wait %s", safetext.ShellSingleQuote(r.session.DeploymentID))
		return printErr("Deployment accepted but recovery state could not be saved", saveErr), false
	}
	if code != 0 {
		r.printRecovery()
		return code, false
	}
	if r.session.Status != statusLive {
		return printErr("Readiness was not confirmed", errors.New("resume the accepted deployment to check its final state")), false
	}
	return 0, true
}

func (r *startRunner) terminal(dep api.DeploymentResponse) int {
	r.progress.Close()
	if !isCompletedDeployment(dep) {
		return printErr("Readiness was not confirmed", errors.New("deployment is still undergoing verification; resume to follow it"))
	}
	dep = deploymentWithReceipt(r.ctx, r.client, dep)
	if dep.ID != r.session.DeploymentID || (r.session.AppID != "" && dep.AppID != "" && dep.AppID != r.session.AppID) {
		return printErr("Deployment identity changed", errors.New("the deployment result does not match the accepted session record"))
	}
	if !isCompletedDeployment(dep) {
		return printErr("Readiness was not confirmed", errors.New("the deployment changed state during verification; resume to inspect it"))
	}
	r.captureDeployment(dep)
	r.session.DeploymentID, r.session.Status = dep.ID, dep.Status
	if dep.AppID != "" {
		r.session.AppID = dep.AppID
	}
	if err := saveStartSession(r.statePath, r.session); err != nil {
		return printErr("Could not save deployment result", err)
	}
	if dep.Status == statusLive {
		if r.progress != nil {
			PrintOK(osStdout, "App ready · %s elapsed", r.progress.elapsed())
		} else {
			PrintOK(osStdout, "App ready")
		}
		return 0
	}
	if dep.Error == "user_error" && dep.ErrorCode == "" {
		PrintFail(osStderr, "Build failed. Choose Show deployment logs to inspect the failing command.")
		return 1
	}
	return renderDeployFailure(dep)
}

func (r *startRunner) resume() int {
	PrintProgress(osStdout, "Resume · %s (deployment %s)", r.session.AppSlug, r.session.DeploymentID)
	app, err := r.client.GetApp(r.ctx, r.session.AppSlug)
	if err != nil {
		return printErr("Could not recover app", err)
	}
	dep, err := r.client.GetDeployment(r.ctx, r.session.DeploymentID)
	if err != nil {
		return printErr("Could not recover deployment", err)
	}
	if dep.AppID != app.ID || (r.session.AppID != "" && r.session.AppID != app.ID) {
		return printErr("Saved deployment does not belong to this app", errors.New("inspect the saved session before starting a new deployment"))
	}
	r.progress = newStartProgress(osStdout, Enabled(), "Waiting for deployment")
	r.progress.observeDeployment(dep)
	defer func() {
		r.progress.Close()
		r.progress = nil
	}()
	var code int
	if isCompletedDeployment(dep) {
		code = r.terminal(dep)
	} else {
		code = streamDeployLogsContextWithOptions(r.ctx, r.client, dep, r.session.AppSlug, streamDeployOptions{
			quiet: true, waitTimeout: r.waitTimeout, onTerminal: r.terminal,
			onStage: r.progress.observeStage, onWaitEnd: r.progress.Close,
		})
	}
	r.progress.Close()
	if code != 0 {
		r.printRecovery()
	}
	return code
}

func (r *startRunner) printRecovery() {
	if r.session.DeploymentID == "" {
		return
	}
	PrintProgress(osStdout, "Run gregale start again from %s to continue this launch.", r.session.ScopePath)
	PrintProgress(osStdout, "Inspect directly: gregale deployment wait %s", safetext.ShellSingleQuote(r.session.DeploymentID))
}

func (r *startRunner) completeLaunch() int {
	PrintProgress(osStdout, "Your app is ready. Checking its first response...")
	code := r.checkFirstResponse(r.suggestedRequestPath(), "")
	if code == 130 {
		return code
	}
	if code == 0 && startTemplateAllowed(r.session.Template) {
		code = r.starterWalkthrough()
	}
	r.finish()
	return code
}

func (r *startRunner) recoverFailure(code int) (int, bool) {
	for code != 130 && code != 3 && r.session.Status == deploymentStatusFailed {
		choice, err := r.prompt.choose(r.ctx, "The deployment failed. Your source and deployment ID are saved.", []string{"I've fixed the code — review and retry", "Show deployment logs", "Finish", "Configure secrets from a file and review retry"}, 0)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return 130, false
			}
			return code, false
		}
		switch choice {
		case 0:
			var ready bool
			code, ready = r.reviewAndDeploy()
			if code == 0 {
				return 0, ready
			}
		case 1:
			_ = runLogs(r.ctx, r.session.AppSlug, r.session.DeploymentID, api.LogFilter{}, nil, false, false)
		case 2:
			return code, false
		case 3:
			if err := r.chooseSecretsFile(); err != nil {
				return startInputExit(err), false
			}
			var ready bool
			code, ready = r.reviewAndDeploy()
			if code == 0 {
				return 0, ready
			}
		}
	}
	return code, false
}

func (r *startRunner) finish() {
	_, _ = fmt.Fprintf(osStdout, "\nKeep building\n  Deploy: gregale deploy --path %s --source=worktree --name %s\n  Logs:   gregale logs %s --follow\n  Open:   gregale open %s\n", safetext.ShellSingleQuote(r.session.SourcePath), safetext.ShellSingleQuote(r.session.AppSlug), safetext.ShellSingleQuote(r.session.AppSlug), safetext.ShellSingleQuote(r.session.AppSlug))
}
