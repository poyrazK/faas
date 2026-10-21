package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

type startRunner struct {
	ctx                 context.Context
	prompt              startPrompt
	secretsFile         string
	waitTimeout         time.Duration
	client              *Client
	account             api.AccountResponse
	session             startSession
	statePath           string
	startedAt           time.Time
	lastRequest         *startRequestResult
	requestFailure      *startRequestFailure
	progress            *startProgress
	target              *startTarget
	configuration       *envRequiredConfiguration
	secretKeys          []string
	sourceBaseline      *startSourceSnapshot
	reviewedSource      *startSourceSnapshot
	projectUpdates      bool
	cancelPendingUpload bool
	// Use the same deploy implementation as deploy/dev. The local seam lets
	// recovery tests observe acceptance without running a remote build.
	deploy func(context.Context, []string, bool, ...deployExecution) int
}

func cmdStart(args []string) (code int) {
	fs := newFlagSet("start", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() > 1 {
		PrintUsage(osStderr, "usage: gregale start [path]; choose one project directory", "deploy")
		return 1
	}
	if jsonOutput {
		return printErr("Interactive session required", errors.New("gregale start does not support --json; use gregale deploy --json for automation"))
	}
	if !stdinIsTTY() {
		return printErr("Interactive session required", errors.New("run gregale start in a terminal; use gregale deploy for scripts"))
	}
	source := "."
	if fs.NArg() == 1 {
		source = fs.Arg(0)
	}
	scope, err := canonicalStartScope(source)
	if err != nil {
		return printErr("Could not resolve session directory", err)
	}
	statePath, err := startSessionPath(scope)
	if err != nil {
		return printErr("Could not locate session", err)
	}
	if err := validateStartScopeBeforeLogin(scope, statePath); err != nil {
		return printErr("Could not read project directory", err)
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
	if code := runner.authenticate(); code != 0 {
		return code
	}
	resume, proceed, err := runner.chooseRecovery()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			return startInputExit(err)
		}
		return printErr("Could not recover session", err)
	}
	if !proceed {
		return 0
	}
	if resume {
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
	ok, err := r.prompt.confirmYes(r.ctx, "Open your browser to sign in")
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
	PrintProgress(osStdout, "2/4 · Find your app")
	root := r.session.ScopePath
	selected, err := r.selectProjectSource(root)
	if err != nil {
		return startInputExit(err)
	}
	if detectShape(selected) != shapeUnknown {
		r.session.SourcePath, r.session.Template, r.session.AppSlug = selected, "", ""
		PrintProgress(osStdout, "Using source %s", selected)
		return 0
	}
	path, template := "", ""
	choices := []string{"Deploy this directory", "Create a starter", "Choose another directory"}
	choice, err := r.prompt.choose(r.ctx, "No app found here. What would you like to deploy?", choices, 1)
	if err != nil {
		return startInputExit(err)
	}
	switch choice {
	case 0:
		path = root
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
		path, err = r.prompt.text(r.ctx, "Starter directory", filepath.Join(root, "gregale-"+strings.TrimPrefix(template, "hello-")))
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

// projectSource is one deployable member of a workspace or convention-shaped
// repository, below the scanned root.
type projectSource struct {
	Name    string // workload name from the scanner
	RootDir string // slash-separated path relative to the scanned root
	Path    string // absolute source directory
}

// discoverProjectSources lists the deployable workspace and convention members
// below root. The root itself is never returned; callers decide whether a
// deployable root is also a candidate.
func discoverProjectSources(root string) ([]projectSource, error) {
	scan, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		return nil, fmt.Errorf("inspect project sources: %w", err)
	}
	var sources []projectSource
	seen := make(map[string]bool)
	for _, workload := range scan.Workloads {
		if workload.RootDir == "" || workload.RootDir == "." || (workload.Tier != reposcan.TierWorkspace && workload.Tier != reposcan.TierConvention) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(workload.RootDir))
		if !seen[path] && detectShape(path) != shapeUnknown {
			seen[path] = true
			sources = append(sources, projectSource{Name: workload.Name, RootDir: workload.RootDir, Path: path})
		}
	}
	return sources, nil
}

func (r *startRunner) selectProjectSource(root string) (string, error) {
	sources, err := discoverProjectSources(root)
	if err != nil {
		return "", err
	}
	var paths, labels []string
	for _, source := range sources {
		paths = append(paths, source.Path)
		labels = append(labels, source.Name+" · "+source.RootDir)
	}
	if len(paths) == 0 {
		return root, nil
	}
	// Workspace metadata alone (for example a package.json with only
	// workspaces) is not another launch candidate. Keep roots with their own
	// runnable app, Dockerfile, or function available in the picker.
	profile, profileErr := frameworkprofile.AnalyzeDir(root)
	if detectShape(root) == shapeFunction || detectFramework(root) == fwDocker || (profileErr == nil && profile.Inferred) {
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
	if r.session.PendingSubmission != nil {
		return printErr("A source submission still needs recovery", errors.New("run gregale start again to recover the pending launch before deploying another revision")), false
	}
	PrintProgress(osStdout, "3/4 · Check source and review deployment")
	if err := r.requireStartCompatibility(r.ctx, r.client); err != nil {
		return printErr("Server compatibility check failed", err), false
	}
	r.target = nil // Refresh app identity and configuration for every reviewed deploy.
	r.reviewedSource = nil
	report, ready, err := r.sourcePreflight()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			return startInputExit(err), false
		}
		return printErr("Could not prepare launch", err), false
	}
	if !ready {
		return 0, false
	}
	if r.session.Template == "" {
		if err := r.prepareSourceReview(); err != nil {
			return printErr("Could not review project files", err), false
		}
	}
	r.session.HealthPath = ""
	if report.Profile != nil && safeStartHealthPath(report.Profile.HealthPath) {
		r.session.HealthPath = report.Profile.HealthPath
	}
	app, exists := r.target.app, r.target.exists
	if edit != nil && (!exists || (r.session.AppID != "" && app.ID != r.session.AppID)) {
		return printErr("App identity changed", errors.New("the starter update must target the app recorded by this session")), false
	}
	accessLabel := "Public URL"
	if api.Plan(r.account.Plan).RequireAuthnDefault() {
		accessLabel = "API token required"
	}
	if exists {
		accessLabel = "Preserve existing access settings"
	}
	action := "Create app and deploy"
	if exists {
		action = "Deploy new revision to existing app"
	}
	_, _ = fmt.Fprintf(osStdout, "\nReady to launch %s\n  Source   %s\n  Account  %s (%s plan)\n  Action   %s\n  Access   %s\n", r.session.AppSlug, r.session.SourcePath, r.account.Email, r.account.Plan, action, accessLabel)
	r.renderLaunchDefaults(report, app, exists)
	if r.session.Project != "" {
		_, _ = fmt.Fprintf(osStdout, "  Project  %s (linked)\n", r.session.Project)
	}
	_, _ = fmt.Fprintf(osStdout, "  Environment  %s\n", scopeOrDefault(r.session.Environment))
	if exists {
		renderDeploymentAccess(osStdout, app, r.session.AppSlug)
	}
	if r.secretsFile != "" {
		_, _ = fmt.Fprintf(osStdout, "  Secrets  Seal these keys before deployment: %s\n", strings.Join(r.secretKeys, ", "))
	}
	if r.configuration != nil && len(r.configuration.ExistingValuesUnverified) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Config   Existing remote keys: %s (values and delivery unverified)\n", strings.Join(r.configuration.ExistingValuesUnverified, ", "))
	}
	r.renderAllowance(exists)
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
	if err := r.verifyReviewedSource(r.ctx); err != nil {
		return printErr("Project files changed after review", err), false
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
	// An explicit empty flag also matters: deploy must not inherit an
	// environment from the original cwd after another source was selected.
	args := []string{"--path", r.session.SourcePath, "--source=worktree", "--name", r.session.AppSlug, "--environment=" + r.session.Environment, "--timeout", strconv.Itoa(int(r.waitTimeout / time.Second)), "--idempotency-key", uuid.NewString()}
	if r.secretsFile != "" {
		args = append(args, "--secrets-file", r.secretsFile)
	}
	deployCtx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	var saveErr, identityErr error
	defer func() {
		r.progress.Close()
		r.progress = nil
	}()
	execution := deployExecution{
		resolveApp:             r.resolveDeployTarget,
		sourcePreflightChecked: true,
		compactProgress:        true,
		onSubmission:           r.recordStartSubmission,
		onUploadSession:        r.recordStartUpload,
		onError:                r.recordStartSubmissionFailure,
		onWaitEnd: func() {
			r.progress.Close()
		},
		onStage: func(name, status string, duration int64, reason string) {
			if r.progress != nil {
				r.progress.observeStage(name, status, duration, reason)
			}
		},
		onQueued: func(dep api.DeploymentResponse) {
			expectedAppID := r.session.AppID
			r.session.DeploymentID, r.session.AppID, r.session.Status = dep.ID, dep.AppID, dep.Status
			r.session.Revision, r.lastRequest = dep.Revision, nil
			r.session.RequireDeploymentEvidence = true
			if (expectedAppID != "" && expectedAppID != dep.AppID) || !r.deploymentScopeMatches(dep) {
				identityErr = errors.New("accepted deployment does not match the reviewed app and environment; inspect its saved ID before continuing")
			}
			if dep.ID != "" && identityErr == nil {
				r.session.PendingSubmission = nil
			}
			if dep.ID == "" {
				saveErr = errors.New("API accepted a deployment without an ID")
			} else {
				saveErr = saveStartSession(r.statePath, r.session)
			}
			if saveErr != nil || identityErr != nil {
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
	if saveErr != nil || identityErr != nil {
		PrintProgress(osStdout, "Inspect accepted deployment directly: %s deployment wait %s", recoveryCLI(), recoveryArgument(r.session.DeploymentID))
		if saveErr != nil {
			return printErr("Deployment accepted but recovery state could not be saved", saveErr), false
		}
		return printErr("Deployment accepted for an unexpected target", identityErr), false
	}
	if code != 0 {
		r.printRecovery()
		return code, false
	}
	if r.session.Status != statusLive {
		return printErr("Readiness was not confirmed", errors.New("resume the accepted deployment to check its final state")), false
	}
	r.sourceBaseline = r.reviewedSource
	return 0, true
}

func (r *startRunner) terminal(dep api.DeploymentResponse) int {
	r.progress.Close()
	if !isCompletedDeployment(dep) {
		return printErr("Readiness was not confirmed", errors.New("deployment is still undergoing verification; resume to follow it"))
	}
	dep = deploymentWithReceipt(r.ctx, r.client, dep)
	if dep.ID != r.session.DeploymentID || (r.session.AppID != "" && dep.AppID != "" && dep.AppID != r.session.AppID) || !r.deploymentScopeMatches(dep) {
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
	if r.session.PendingSubmission != nil {
		if err := r.recoverStartSubmission(); err != nil {
			r.printRecovery()
			return printErr("Could not recover source submission", err)
		}
	}
	PrintProgress(osStdout, "Resume · %s (deployment %s)", r.session.AppSlug, r.session.DeploymentID)
	if err := r.verifyStartProject(r.ctx, r.client); err != nil {
		return printErr("Could not recover linked target", err)
	}
	app, err := r.getLaunchApp(r.ctx, r.client, r.session.AppSlug)
	if err != nil {
		return printErr("Could not recover app", err)
	}
	dep, err := r.client.GetDeployment(r.ctx, r.session.DeploymentID)
	if err != nil {
		return printErr("Could not recover deployment", err)
	}
	if dep.AppID != app.ID || (r.session.AppID != "" && r.session.AppID != app.ID) || !r.deploymentScopeMatches(dep) {
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
	if pending := r.session.PendingSubmission; pending != nil {
		PrintProgress(osStdout, "Check the pending launch: %s start %s", recoveryCLI(), recoveryArgument(r.session.ScopePath))
		PrintProgress(osStdout, "Inspect app history: %s deployments --app %s", recoveryCLI(), recoveryArgument(r.session.AppSlug))
		if r.session.DeploymentID != "" {
			PrintProgress(osStdout, "Previously accepted deployment: %s deployment %s", recoveryCLI(), recoveryArgument(r.session.DeploymentID))
		}
		r.printPreviouslyVerified()
		return
	}
	if r.session.DeploymentID == "" {
		return
	}
	PrintProgress(osStdout, "Continue this launch: %s start %s", recoveryCLI(), recoveryArgument(r.session.ScopePath))
	PrintProgress(osStdout, "Inspect directly: %s deployment wait %s", recoveryCLI(), recoveryArgument(r.session.DeploymentID))
	r.printPreviouslyVerified()
}

func (r *startRunner) completeLaunch() int {
	PrintProgress(osStdout, "Your app is ready. Checking its first response...")
	code := r.checkFirstResponse(r.suggestedRequestPath(), "")
	if code == 130 {
		return code
	}
	if code == 0 {
		code = r.rememberVerifiedDeployment()
	}
	if code == 0 {
		if startTemplateAllowed(r.session.Template) {
			code = r.starterWalkthrough()
		} else {
			code = r.projectWalkthrough()
		}
	}
	r.finish()
	return code
}

func (r *startRunner) recoverFailure(code int) (int, bool) {
	for code != 130 && code != 3 && r.session.Status == deploymentStatusFailed && r.session.PendingSubmission == nil {
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
	if err := r.rememberApp(); err != nil {
		PrintWarn(osStdout, "App is live, but its local default could not be saved: %v", err)
	}
	cli, source, app := recoveryCLI(), recoveryArgument(r.session.SourcePath), recoveryArgument(r.session.AppSlug)
	_, _ = fmt.Fprintf(osStdout, "\nKeep building\n  Update: %s deploy --path %s --source=worktree --name %s --environment=%s\n  Logs:   %s logs %s --deployment %s --follow\n", cli, source, app, recoveryArgument(r.session.Environment), cli, app, recoveryArgument(r.session.DeploymentID))
	if r.session.Environment == "" {
		_, _ = fmt.Fprintf(osStdout, "  Open:   %s open %s\n", cli, app)
	} else if r.lastRequest != nil && r.lastRequest.httpStatus != 0 && !r.lastRequest.private {
		_, _ = fmt.Fprintf(osStdout, "  Open URL: %s\n", startResponseText(r.lastRequest.appURL))
	} else {
		_, _ = fmt.Fprintf(osStdout, "  Inspect: %s deployment %s\n", cli, recoveryArgument(r.session.DeploymentID))
	}
	_, _ = fmt.Fprintf(osStdout, "  Delete app: %s apps rm %s\n", cli, app)
	r.printPreviouslyVerified()
}
