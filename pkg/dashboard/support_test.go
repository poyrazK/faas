package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/apid/supportportal"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestSupportPagesRenderScopedFormsAndEscapeContent(t *testing.T) {
	attack := `<script>alert("private")</script>`
	now := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	ticket := api.SupportTicket{ID: "case-id", Subject: attack, Status: "open", Category: "deployment", Revision: 7, CreatedAt: now, UpdatedAt: now}
	staffTicket := support.StaffTicket{Ticket: ticket, Priority: "high", AssigneeID: "agent", Episode: support.Episode{FirstResponseDue: now, ResolutionDue: now.Add(time.Hour)}}
	for _, test := range []struct {
		name         string
		data         supportportal.Data
		want, absent []string
	}{
		{"customer list", supportportal.Data{Base: "/dashboard/orgs/acme/support", Scope: "Organization: acme", PortalView: support.PortalView{Tickets: api.SupportTicketPage{Items: []api.SupportTicket{ticket}}}}, []string{`href="/dashboard/orgs/acme/support/case-id"`, `action="/dashboard/orgs/acme/support"`, `name="category"`, "Start a conversation"}, []string{"Internal note", "Resolution target", "/dashboard/admin/support"}},
		{"customer detail", supportportal.Data{Base: "/dashboard/support", PortalView: support.PortalView{Ticket: &ticket, Messages: api.SupportMessagePage{Items: []api.SupportMessage{{Body: attack}}}}}, []string{`name="expected_revision" value="7"`, `action="/dashboard/support/case-id"`, "Mark resolved", "Send reply", "&lt;script&gt;"}, []string{"Internal note", "Assignment and priority"}},
		{"staff detail", supportportal.Data{Base: "/dashboard/admin/support", StaffMode: true, ActorID: "agent", PortalView: support.PortalView{Role: "agent", Ticket: &ticket, StaffTicket: &staffTicket, Messages: api.SupportMessagePage{Items: []api.SupportMessage{{Author: "note", Body: attack}}}}}, []string{"Internal note · staff only", "Assignment and priority", "First reply: 12 Nov 2026", "Wait for customer"}, []string{"How was your support experience?"}},
		{"staff read only", supportportal.Data{Base: "/dashboard/admin/support", StaffMode: true, ActorID: "someone-else", PortalView: support.PortalView{Role: "agent", Ticket: &ticket, StaffTicket: &staffTicket}}, []string{"Resolution target"}, []string{"Send reply", "Save internal note", "Mark resolved", "Save assignment"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.data.Filters = url.Values{}
			test.data.Keys = map[string]string{"reply": "saved-key"}
			w := httptest.NewRecorder()
			if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", dashboard.Page{Title: "Support", Body: "support", Data: test.data}); err != nil {
				t.Fatal(err)
			}
			body := w.Body.String()
			if strings.Contains(body, attack) {
				t.Fatal("unescaped support content")
			}
			for _, want := range test.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
			if !strings.Contains(body, `nonce="test-nonce"`) {
				t.Fatal("CSP nonce missing")
			}
		})
	}
}

func TestSupportResolvedEpisodeAndPausedClock(t *testing.T) {
	now := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	ticket := api.SupportTicket{ID: "case-id", Status: "resolved", Revision: 8, CanReopen: true, ReopenUntil: &now}
	d := supportportal.Data{Base: "/dashboard/support", Filters: url.Values{}, Keys: map[string]string{}, PortalView: support.PortalView{Ticket: &ticket, Satisfaction: &api.SupportSatisfaction{EpisodeID: "episode", Eligible: true}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.Default(), "", dashboard.Page{Body: "support", Data: d}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Reopen conversation", `name="episode_id" value="episode"`, "Send rating"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "Send reply") {
		t.Fatal("resolved ticket offered reply")
	}
	episode := support.Episode{WaitSince: &now, FirstResponseDue: now.Add(-time.Hour), ResolutionDue: now.Add(time.Hour)}
	if d.ResponseTarget(episode) != "Waiting for customer" || d.ResolutionTarget(episode) != "Paused while waiting for customer" {
		t.Fatal("paused clocks shown as live deadlines")
	}
}

func TestSupportKeepsDraftWhenConcurrentResolutionHidesReplyForm(t *testing.T) {
	ticket := api.SupportTicket{ID: "case-id", Status: "resolved", Revision: 2}
	d := supportportal.Data{Base: "/dashboard/support", Filters: url.Values{}, Keys: map[string]string{},
		PortalView: support.PortalView{Ticket: &ticket}, Failed: "reply", Error: "Ticket revision changed",
		Form: url.Values{"body": {"Unsent <script>draft</script>"}, "idempotency_key": {"original-request-key"}, "expected_revision": {"1"}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.Default(), "", dashboard.Page{Body: "support", Data: d}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Your submitted message", "Unsent &lt;script&gt;draft&lt;/script&gt;", "original-request-key", "Revision: 1"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("lost submitted draft field %q", want)
		}
	}
}
