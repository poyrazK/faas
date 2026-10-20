package main

import (
	"os"
	"testing"
)

// Internal: it drives the start runner's session files directly.
func TestStartRememberMovesPendingRecoveryWithoutChangingOtherAccounts(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	runner := startTestRunner(t, source, "")
	runner.session.AppID, runner.session.DeploymentID, runner.session.VerifiedDeploymentID = "app-1", "previous-1", "previous-1"
	runner.session.PendingSubmission = &startPendingSubmission{Key: "11111111-1111-4111-8111-111111111111", Transport: "resumable", UploadID: "pending-upload", TotalSize: 512}
	previousPath := runner.statePath
	if err := saveStartSession(previousPath, runner.session); err != nil {
		t.Fatal(err)
	}
	other := runner.session
	other.AccountID, other.AppID, other.DeploymentID = "other-account", "other-app", "other-deployment"
	other.PendingSubmission = nil
	otherPath := startContextSessionPath(previousPath, other.APIBase, other.AccountID)
	if err := saveStartSession(otherPath, other); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.rememberApp(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previousPath); !os.IsNotExist(err) {
		t.Fatalf("previous pending record was not retired: %v", err)
	}
	after, err := os.ReadFile(otherPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("another account's recovery record changed: %v", err)
	}
	recovery := startTestRunner(t, source, "\n")
	resume, proceed, err := recovery.chooseRecovery()
	saved := recovery.session
	if err != nil || !resume || !proceed || saved.DeploymentID != "previous-1" || saved.VerifiedDeploymentID != "previous-1" || saved.PendingSubmission == nil || *saved.PendingSubmission != *runner.session.PendingSubmission {
		t.Fatalf("pending recovery changed while remembering the app: resume=%t proceed=%t session=%+v err=%v", resume, proceed, saved, err)
	}
}
