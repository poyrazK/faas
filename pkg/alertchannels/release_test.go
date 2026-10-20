package alertchannels

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// adr: 20261020085706 — release approval notices render safely on every channel.

func requestedNotice() ReleaseNotice {
	return ReleaseNotice{Event: EventApprovalRequested, App: "shop", DeploymentID: "3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10",
		Revision: 42, DeployedBy: "ana <@U123> & co", Commit: "a1b2c3d", Reason: "checkout fix", Stage: 1, TotalStages: 4,
		TrafficPercent: 5, Approvals: 1, Required: 2, ExpiresAt: "2026-10-21T09:00:00Z",
		ReviewURL: "https://console.gregale.dev/dashboard/releases/3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10/approval"}
}

func TestReleaseSlackPayloadButtonsAndEscaping(t *testing.T) {
	payload := SlackPayload(ReleaseMessage(requestedNotice(), time.Now()))
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"Approval needed: release v42 of shop", "Waiting for 1 more approval(s) (1 of 2) at 5% traffic, canary stage 1 of 4.",
		`"action_id":"release_approve"`, `"action_id":"release_reject"`, `"action_id":"release_review"`,
		"approval?intent=approve", "approval?intent=reject", "ana \\u0026lt;@U123\\u0026gt; \\u0026amp; co"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Slack payload lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<@U123>") {
		t.Fatalf("customer text formed a Slack mention: %s", body)
	}

	approved := requestedNotice()
	approved.Event, approved.Actor, approved.Approvals = EventApprovalApproved, "bo@example.com", 2
	raw, _ = json.Marshal(SlackPayload(ReleaseMessage(approved, time.Now())))
	if body := string(raw); !strings.Contains(body, "Approved by bo@example.com (2 of 2)") || strings.Contains(body, "release_approve") ||
		!strings.Contains(body, "release_review") {
		t.Fatalf("approved payload = %s", body)
	}

	insecure := requestedNotice()
	insecure.ReviewURL = "http://console.gregale.dev/dashboard/releases/x/approval"
	raw, _ = json.Marshal(SlackPayload(ReleaseMessage(insecure, time.Now())))
	if strings.Contains(string(raw), `"actions"`) {
		t.Fatalf("a non-https review URL must not become a button: %s", raw)
	}
}

func TestReleaseEmailAndPagerDuty(t *testing.T) {
	subject, body := EmailContent(ReleaseMessage(requestedNotice(), time.Now()))
	if subject != "[Gregale] Approval needed: release v42 of shop" || !strings.Contains(body, "Approve: https://console.gregale.dev/") ||
		!strings.Contains(body, "gregale approvals approve 3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10") {
		t.Fatalf("email = %q\n%s", subject, body)
	}
	rejected := requestedNotice()
	rejected.Event, rejected.Actor, rejected.Comment = EventApprovalRejected, "cy@example.com", "latency is up"
	if subject, body := EmailContent(ReleaseMessage(rejected, time.Now())); !strings.HasPrefix(subject, "[Gregale] Rejected:") ||
		!strings.Contains(body, "Comment: latency is up") || strings.Contains(body, "Approve:") {
		t.Fatalf("rejected email = %q\n%s", subject, body)
	}
	s := &Sender{}
	err := s.Send(context.Background(), Target{Kind: KindPagerDuty, RoutingKey: strings.Repeat("a", 32), Region: RegionUS}, ReleaseMessage(requestedNotice(), time.Now()))
	if !errors.Is(err, ErrReleaseNoticeKind) {
		t.Fatalf("PagerDuty send err = %v, want ErrReleaseNoticeKind", err)
	}
}
