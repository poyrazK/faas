package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Keep the legacy directory record readable. A different API/account gets a
// separate record, so returning to the original context can still resume it.
func startContextSessionPath(path, apiBase, account string, linkedContext ...string) string {
	identity := apiBase + "\x00" + account
	if len(linkedContext) > 0 && linkedContext[0] != "" {
		identity += "\x00" + linkedContext[0]
	}
	key := sha256.Sum256([]byte(identity))
	return strings.TrimSuffix(path, ".json") + "-" + hex.EncodeToString(key[:]) + ".json"
}

func (r *startRunner) chooseRecovery() (resume, proceed bool, err error) {
	_, key, err := startSourceContext(r.session.ScopePath)
	if err != nil {
		return false, false, err
	}
	r.session.ContextKey = key
	contextPath := startContextSessionPath(r.statePath, r.session.APIBase, r.account.ID, key)
	saved, found, err := loadStartSession(contextPath)
	if err != nil {
		return false, false, err
	}
	if found {
		r.statePath = contextPath
	} else {
		saved, found, err = loadStartSession(r.statePath)
		if err != nil {
			return false, false, err
		}
	}
	if !found {
		return false, true, nil
	}
	scope, err := canonicalStartScope(saved.ScopePath)
	if err != nil || scope != r.session.ScopePath {
		return false, false, errors.New("the saved session belongs to another directory")
	}
	if saved.APIBase != r.session.APIBase || saved.AccountID != r.account.ID || saved.ContextKey != key {
		PrintProgress(osStdout, "A saved launch belongs to another API, account, or linked target. Its recovery record will be kept.")
		choice, err := r.prompt.choose(r.ctx, "Use the current context for a separate launch?", []string{"Start a new launch", "Finish and keep the saved launch"}, 1)
		if err != nil || choice == 1 {
			return false, false, err
		}
		r.statePath = contextPath
		return false, true, nil
	}
	if saved.PendingSubmission != nil {
		label := "Check pending launch for " + saved.AppSlug
		if saved.Project != "" {
			label += " (" + saved.Project + ", environment " + scopeOrDefault(saved.Environment) + ")"
		}
		choices := []string{label, "Finish and keep the recovery record"}
		if saved.PendingSubmission.Transport == "resumable" && saved.PendingSubmission.UploadID != "" {
			choices = append(choices, "Cancel an unfinished upload so source can be reviewed again")
		}
		choice, err := r.prompt.choose(r.ctx, "A source submission needs recovery before another launch.", choices, 0)
		if err != nil || choice == 1 {
			return false, false, err
		}
		r.cancelPendingUpload = choice == 2
		r.session = saved
		return true, true, nil
	}
	if saved.DeploymentID == "" {
		return false, true, nil
	}
	label := "Continue " + saved.AppSlug
	if saved.Project != "" {
		label += " (" + saved.Project + ", environment " + scopeOrDefault(saved.Environment) + ")"
	}
	choice, err := r.prompt.choose(r.ctx, "Welcome back.", []string{label, "Start a new launch"}, 0)
	if err != nil {
		return false, false, err
	}
	if choice == 0 {
		r.session = saved
	}
	return choice == 0, true, nil
}
