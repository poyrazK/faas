package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021220456 — `edge-rules attack-mode` maps on, off and status to
// PUT, DELETE and GET on the switch, sends --for in seconds, and refuses a
// bad verb or duration before any request.
func TestEdgeRulesAttackModeCommand(t *testing.T) {
	main.ResetJSONOut(t)
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = io.Discard
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	const path = "/v1/apps/demo/edge-protection/attack-mode"

	f := main.AuthedFakeAPI(t, `{"active":true,"rule_id":"r1"}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"attack-mode", "on", "--app", "demo", "--for", "6h"}); code != 0 {
		t.Fatalf("on: exit = %d", code)
	}
	var req api.EnableEdgeAttackModeRequest
	if (*f.SawMethod()) != http.MethodPut || (*f.SawPath()) != path ||
		json.Unmarshal((*f.SawBody()), &req) != nil || req.DurationSeconds != 6*3600 {
		t.Fatalf("on: request = %s %s %s", (*f.SawMethod()), (*f.SawPath()), (*f.SawBody()))
	}

	for verb, method := range map[string]string{"off": http.MethodDelete, "status": http.MethodGet} {
		f = main.AuthedFakeAPI(t, `{"active":false}`, http.StatusOK)
		if code := main.CmdEdgeRules([]string{"attack-mode", verb, "--app", "demo"}); code != 0 {
			t.Fatalf("%s: exit = %d", verb, code)
		}
		if (*f.SawMethod()) != method || (*f.SawPath()) != path {
			t.Fatalf("%s: request = %s %s", verb, (*f.SawMethod()), (*f.SawPath()))
		}
	}
	f = main.AuthedFakeAPI(t, `{"active":false}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"attack-mode", "--app", "demo"}); code != 0 || (*f.SawMethod()) != http.MethodGet {
		t.Fatalf("no verb: exit %d, method %q; want a status read", code, (*f.SawMethod()))
	}

	for name, args := range map[string][]string{
		"unknown verb":   {"attack-mode", "maybe", "--app", "demo"},
		"two verbs":      {"attack-mode", "on", "off", "--app", "demo"},
		"too short":      {"attack-mode", "on", "--app", "demo", "--for", "5m"},
		"too long":       {"attack-mode", "on", "--app", "demo", "--for", "48h"},
		"fractional sec": {"attack-mode", "on", "--app", "demo", "--for", "1h0.5s"},
	} {
		f = main.AuthedFakeAPI(t, `{}`, http.StatusOK)
		if code := main.CmdEdgeRules(args); code == 0 || (*f.SawMethod()) != "" {
			t.Errorf("%s: exit %d, request %q; want failure before any request", name, code, (*f.SawMethod()))
		}
	}
}

func TestRenderEdgeAttackMode(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	until := now.Add(90 * time.Minute)
	var buf bytes.Buffer
	main.RenderEdgeAttackMode(&buf, "shop", api.EdgeAttackModeStatus{Active: true, ExpiresAt: &until}, now)
	for _, want := range []string{"Attack mode is ON for shop until", "(1h30m0s left)", "403 challenge_required", "attack-mode off --app shop"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("on output missing %q:\n%s", want, buf.String())
		}
	}

	ended := now.Add(-time.Hour)
	buf.Reset()
	main.RenderEdgeAttackMode(&buf, "shop", api.EdgeAttackModeStatus{ExpiresAt: &ended}, now)
	if !strings.Contains(buf.String(), "Attack mode is off for shop (last ended") {
		t.Fatalf("lapsed output = %q", buf.String())
	}
	buf.Reset()
	main.RenderEdgeAttackMode(&buf, "shop", api.EdgeAttackModeStatus{}, now)
	if buf.String() != "Attack mode is off for shop.\n" {
		t.Fatalf("off output = %q", buf.String())
	}

	// The summary leads with attack mode, even when the counts are degraded.
	buf.Reset()
	main.RenderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "degraded: prometheus unavailable",
		AttackMode: api.EdgeAttackModeStatus{Active: true, ExpiresAt: &until}})
	if !strings.Contains(buf.String(), "attack mode:") || !strings.Contains(buf.String(), "ON until") {
		t.Fatalf("summary output = %q, want the attack mode line", buf.String())
	}
}
