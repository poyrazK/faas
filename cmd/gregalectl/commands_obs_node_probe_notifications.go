package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func fetchObsNodeProbeNotifications(ctx context.Context, client *http.Client, base, node string, credential obsCredential) (*api.ObsNodeProbeNotifications, string) {
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var out api.ObsNodeProbeNotifications
	if err := fetchObsBundleSection(ctx, &noRedirect, base+url.PathEscape(node)+"/probe-notifications", credential, "probe notifications", &out); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 405") || strings.Contains(err.Error(), "HTTP 501") {
			return nil, "server_does_not_report_probe_notifications"
		}
		return nil, "probe_notifications_unavailable"
	}
	if out.Node != node || !operatorobs.NodeProbeNotificationsValid(out) {
		return nil, "invalid_probe_notifications"
	}
	return &out, ""
}

func writeObsNodeProbeNotifications(w io.Writer, page obsNodePage) {
	out := page.ProbeNotifications
	if out == nil {
		reason := page.ProbeNotificationsNote
		if reason == "" {
			reason = "server_does_not_report_probe_notifications"
		}
		_, _ = fmt.Fprintf(w, "probe notifications  unknown: %s\n", reason)
		return
	}
	expected := "unknown"
	if out.Expected != nil {
		expected = strconv.FormatBool(*out.Expected)
	}
	_, _ = fmt.Fprintf(w, "probe notifications  %s reason=%s expected=%s generated_at=%s\n", out.State, out.Reason, expected, servingTime(&out.GeneratedAt))
	writeServingProbeNotificationItems(w, out.Channels)
	_, _ = fmt.Fprintln(w, "  evidence=last_completed_submission provider_receipt=unassessed human_receipt=unassessed")
}
