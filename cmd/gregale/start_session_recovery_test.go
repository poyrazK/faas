package main

import (
	"bytes"
	"os"
	"testing"
)

func TestStartDifferentContextKeepsBothRecoveryRecords(t *testing.T) {
	for _, mismatch := range []string{"API", "account"} {
		t.Run(mismatch, func(t *testing.T) {
			startTestEnvironment(t)
			source := startTestSource(t)
			runner := startTestRunner(t, source, "1\n")
			original := runner.session
			original.DeploymentID, original.AppID, original.Status = "old-deploy", "old-app", "pending"
			if mismatch == "API" {
				original.APIBase = "https://previous.example.test"
			} else {
				original.AccountID = "previous-account"
			}
			legacyPath := runner.statePath
			if err := saveStartSession(legacyPath, original); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(legacyPath)
			resume, proceed, err := runner.chooseRecovery()
			if err != nil || resume || !proceed || runner.statePath == legacyPath {
				t.Fatalf("new context was blocked: resume=%t proceed=%t err=%v", resume, proceed, err)
			}
			runner.session.DeploymentID, runner.session.AppID, runner.session.Status = "new-deploy", "new-app", "live"
			if err := saveStartSession(runner.statePath, runner.session); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(legacyPath)
			if !bytes.Equal(before, after) {
				t.Fatal("starting under another context overwrote the accepted deployment")
			}
			for _, old := range []bool{true, false} {
				recovery := startTestRunner(t, source, "\n")
				want := "new-deploy"
				if old {
					recovery.account.ID, recovery.session.APIBase = original.AccountID, original.APIBase
					want = "old-deploy"
				}
				resume, proceed, err := recovery.chooseRecovery()
				if err != nil || !resume || !proceed || recovery.session.DeploymentID != want {
					t.Fatalf("wrong context resumed: want=%s got=%s resume=%t err=%v", want, recovery.session.DeploymentID, resume, err)
				}
			}
		})
	}
}

func TestStartDifferentContextFinishAndEOFKeepRecovery(t *testing.T) {
	for _, input := range []string{"\n", ""} {
		t.Run(map[bool]string{true: "finish", false: "EOF"}[input != ""], func(t *testing.T) {
			startTestEnvironment(t)
			runner := startTestRunner(t, startTestSource(t), input)
			original := runner.session
			original.AccountID, original.DeploymentID = "previous-account", "old-deploy"
			if err := saveStartSession(runner.statePath, original); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(runner.statePath)
			resume, proceed, _ := runner.chooseRecovery()
			after, _ := os.ReadFile(runner.statePath)
			if resume || proceed || !bytes.Equal(before, after) {
				t.Fatal("finish/EOF resumed or modified a saved launch")
			}
		})
	}
}

func TestStartLinkedContextKeepsRecoveryForEachTarget(t *testing.T) {
	for _, changed := range []string{"project", "app", "environment"} {
		t.Run(changed, func(t *testing.T) {
			startTestEnvironment(t)
			source := startTestSource(t)
			startTestLink(t, source, "shop", "shop-api", "staging")
			original := startTestRunner(t, source, "")
			if _, proceed, err := original.chooseRecovery(); err != nil || !proceed {
				t.Fatalf("initial context: proceed=%t err=%v", proceed, err)
			}
			original.session.AppSlug, original.session.DeploymentID = "shop-api", "old-deploy"
			original.session.Project, original.session.Environment = "shop", "staging"
			if err := saveStartSession(original.statePath, original.session); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(original.statePath)
			project, app, environment := "shop", "shop-api", "staging"
			switch changed {
			case "project":
				project = "other-project"
			case "app":
				app = "shop-worker"
			case "environment":
				environment = "production"
			}
			startTestLink(t, source, project, app, environment)
			next := startTestRunner(t, source, "1\n")
			if resume, proceed, err := next.chooseRecovery(); err != nil || resume || !proceed || next.statePath == original.statePath {
				t.Fatalf("target switch reused recovery: resume=%t proceed=%t err=%v", resume, proceed, err)
			}
			next.session.AppSlug, next.session.DeploymentID = app, "new-deploy"
			next.session.Project, next.session.Environment = project, environment
			if err := saveStartSession(next.statePath, next.session); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(original.statePath)
			if !bytes.Equal(before, after) {
				t.Fatal("linked target switch overwrote an accepted deployment")
			}
			for _, old := range []bool{true, false} {
				want := "new-deploy"
				if old {
					startTestLink(t, source, "shop", "shop-api", "staging")
					want = "old-deploy"
				} else {
					startTestLink(t, source, project, app, environment)
				}
				recovery := startTestRunner(t, source, "\n")
				resume, proceed, err := recovery.chooseRecovery()
				if err != nil || !resume || !proceed || recovery.session.DeploymentID != want {
					t.Fatalf("wrong linked launch resumed: want=%s got=%s resume=%t err=%v", want, recovery.session.DeploymentID, resume, err)
				}
			}
		})
	}
}
