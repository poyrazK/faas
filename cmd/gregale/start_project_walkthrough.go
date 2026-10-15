package main

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/safetext"
)

func (r *startRunner) projectWalkthrough() int {
	r.projectUpdates = true
	defer func() {
		r.projectUpdates = false
		r.reviewedSource = nil
	}()
	_, _ = fmt.Fprintf(osStdout, "\nEdit your project in another terminal or editor:\n  %s\n", startResponseText(r.session.SourcePath))
	for {
		choice, err := r.prompt.choose(r.ctx, "Keep building in this project.", []string{"Finish", "I've made changes — review and deploy again"}, 0)
		if err != nil {
			return startInputExit(err)
		}
		if choice == 0 {
			return 0
		}
		path := r.suggestedRequestPath()
		if r.lastRequest != nil {
			path = r.lastRequest.path
		}
		code, submitted := r.reviewAndDeploy()
		if code != 0 {
			code, submitted = r.recoverFailure(code)
		}
		if code != 0 || !submitted {
			return code
		}
		PrintProgress(osStdout, "Checking GET %s on the new deployment...", startResponseText(path))
		if code := r.checkFirstResponse(path, ""); code != 0 {
			return code
		}
		if code := r.rememberVerifiedDeployment(); code != 0 {
			return code
		}
		PrintOK(osStdout, "Your updated project answered from deployment %s.", r.session.DeploymentID)
	}
}

func (r *startRunner) rememberVerifiedDeployment() int {
	if r.lastRequest == nil || r.session.DeploymentID == "" {
		return printErr("Could not save the verified deployment", errors.New("no successful response check is available"))
	}
	previous := r.session.VerifiedDeploymentID
	r.session.VerifiedDeploymentID = r.session.DeploymentID
	if err := saveStartSession(r.statePath, r.session); err != nil {
		r.session.VerifiedDeploymentID = previous
		return printErr("Could not save the verified deployment", err)
	}
	return 0
}

func (r *startRunner) printPreviouslyVerified() {
	if previous := r.session.VerifiedDeploymentID; previous != "" && previous != r.session.DeploymentID {
		PrintProgress(osStdout, "Previous successful check: gregale deployment %s", safetext.ShellSingleQuote(previous))
	}
}
