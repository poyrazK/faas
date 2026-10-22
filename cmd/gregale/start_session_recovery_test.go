package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"testing"
)

func TestStartDifferentContextKeepsBothRecoveryRecords(t *testing.T) {
	for _, mismatch := range []string{"API", "account"} {
		t.Run(mismatch, func(t *testing.T) {
			main.StartTestEnvironment(t)
			source := main.StartTestSource(t)
			runner := main.StartTestRunner(t, source, "1\n")
			original := (*runner.Session())
			original.DeploymentID, original.AppID, original.Status = "old-deploy", "old-app", "pending"
			if mismatch == "API" {
				original.APIBase = "https://previous.example.test"
			} else {
				original.AccountID = "previous-account"
			}
			legacyPath := (*runner.StatePath())
			if err := main.SaveStartSession(legacyPath, original); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(legacyPath)
			resume, proceed, err := runner.ChooseRecovery()
			if err != nil || resume || !proceed || (*runner.StatePath()) == legacyPath {
				t.Fatalf("new context was blocked: resume=%t proceed=%t err=%v", resume, proceed, err)
			}
			(*runner.Session()).DeploymentID, (*runner.Session()).AppID, (*runner.Session()).Status = "new-deploy", "new-app", "live"
			if err := main.SaveStartSession((*runner.StatePath()), (*runner.Session())); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(legacyPath)
			if !bytes.Equal(before, after) {
				t.Fatal("starting under another context overwrote the accepted deployment")
			}
			for _, old := range []bool{true, false} {
				recovery := main.StartTestRunner(t, source, "\n")
				want := "new-deploy"
				if old {
					(*recovery.Account()).ID, (*recovery.Session()).APIBase = original.AccountID, original.APIBase
					want = "old-deploy"
				}
				resume, proceed, err := recovery.ChooseRecovery()
				if err != nil || !resume || !proceed || (*recovery.Session()).DeploymentID != want {
					t.Fatalf("wrong context resumed: want=%s got=%s resume=%t err=%v", want, (*recovery.Session()).DeploymentID, resume, err)
				}
			}
		})
	}
}

func TestStartDifferentContextFinishAndEOFKeepRecovery(t *testing.T) {
	for _, input := range []string{"\n", ""} {
		t.Run(map[bool]string{true: "finish", false: "EOF"}[input != ""], func(t *testing.T) {
			main.StartTestEnvironment(t)
			runner := main.StartTestRunner(t, main.StartTestSource(t), input)
			original := (*runner.Session())
			original.AccountID, original.DeploymentID = "previous-account", "old-deploy"
			if err := main.SaveStartSession((*runner.StatePath()), original); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile((*runner.StatePath()))
			resume, proceed, _ := runner.ChooseRecovery()
			after, _ := os.ReadFile((*runner.StatePath()))
			if resume || proceed || !bytes.Equal(before, after) {
				t.Fatal("finish/EOF resumed or modified a saved launch")
			}
		})
	}
}

func TestStartLinkedContextKeepsRecoveryForEachTarget(t *testing.T) {
	for _, changed := range []string{"project", "app", "environment"} {
		t.Run(changed, func(t *testing.T) {
			main.StartTestEnvironment(t)
			source := main.StartTestSource(t)
			main.StartTestLink(t, source, "shop", "shop-api", "staging")
			original := main.StartTestRunner(t, source, "")
			if _, proceed, err := original.ChooseRecovery(); err != nil || !proceed {
				t.Fatalf("initial context: proceed=%t err=%v", proceed, err)
			}
			(*original.Session()).AppSlug, (*original.Session()).DeploymentID = "shop-api", "old-deploy"
			(*original.Session()).Project, (*original.Session()).Environment = "shop", "staging"
			if err := main.SaveStartSession((*original.StatePath()), (*original.Session())); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile((*original.StatePath()))
			project, app, environment := "shop", "shop-api", "staging"
			switch changed {
			case "project":
				project = "other-project"
			case "app":
				app = "shop-worker"
			case "environment":
				environment = "production"
			}
			main.StartTestLink(t, source, project, app, environment)
			next := main.StartTestRunner(t, source, "1\n")
			if resume, proceed, err := next.ChooseRecovery(); err != nil || resume || !proceed || (*next.StatePath()) == (*original.StatePath()) {
				t.Fatalf("target switch reused recovery: resume=%t proceed=%t err=%v", resume, proceed, err)
			}
			(*next.Session()).AppSlug, (*next.Session()).DeploymentID = app, "new-deploy"
			(*next.Session()).Project, (*next.Session()).Environment = project, environment
			if err := main.SaveStartSession((*next.StatePath()), (*next.Session())); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile((*original.StatePath()))
			if !bytes.Equal(before, after) {
				t.Fatal("linked target switch overwrote an accepted deployment")
			}
			for _, old := range []bool{true, false} {
				want := "new-deploy"
				if old {
					main.StartTestLink(t, source, "shop", "shop-api", "staging")
					want = "old-deploy"
				} else {
					main.StartTestLink(t, source, project, app, environment)
				}
				recovery := main.StartTestRunner(t, source, "\n")
				resume, proceed, err := recovery.ChooseRecovery()
				if err != nil || !resume || !proceed || (*recovery.Session()).DeploymentID != want {
					t.Fatalf("wrong linked launch resumed: want=%s got=%s resume=%t err=%v", want, (*recovery.Session()).DeploymentID, resume, err)
				}
			}
		})
	}
}
