package main

import (
	"strings"
	"testing"
	"time"
)

func TestProjectDashboardGitHubActivityUsesSafeStatusAndLinks(t *testing.T) {
	sha := strings.Repeat("a", 40)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	view := projectDashboardGitHubActivity(&githubActivityResponse{
		WebhookDeliveries: []githubWebhookActivityResponse{{
			EventType: "push", Status: "dead", CommitSHA: sha, ReceivedAt: now,
		}},
		CheckUpdates: []githubCheckActivityResponse{{
			DeploymentID: "deployment-1", Status: "succeeded", CommitSHA: sha, UpdatedAt: now,
		}},
	}, "acme/api", "demo")

	if view == nil || len(view.WebhookDeliveries) != 1 || len(view.CheckUpdates) != 1 {
		t.Fatalf("view = %+v", view)
	}
	webhook := view.WebhookDeliveries[0]
	if webhook.Status != "needs attention" || webhook.StatusClass != "cert-failed" {
		t.Fatalf("webhook status = (%q, %q)", webhook.Status, webhook.StatusClass)
	}
	if !strings.Contains(webhook.CommitURL, "github.com/acme/api/commit/"+sha) {
		t.Fatalf("commit URL = %q", webhook.CommitURL)
	}
	check := view.CheckUpdates[0]
	if check.Status != "synced" || check.StatusClass != "running" {
		t.Fatalf("check status = (%q, %q)", check.Status, check.StatusClass)
	}
	if check.DeploymentURL != "/dashboard/apps/demo/deployments/deployment-1" {
		t.Fatalf("deployment URL = %q", check.DeploymentURL)
	}
}
