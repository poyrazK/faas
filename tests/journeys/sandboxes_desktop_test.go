package journeys

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// desktop: a session created with preset desktop runs a desktop at its
// resolution that gregale-desktop drives, and serves its noVNC views on
// private preview ports, each behind its own token (ADR-20261124075005). It
// runs on its own session. Skips where desktops are switched off (no
// desktop image) or the plan has none.
func (j *sandboxJourney) desktop(t *testing.T) {
	body := map[string]any{"preset": "desktop", "resolution": "1024x768", "network": map[string]any{"mode": "none"},
		"labels": map[string]string{"e2ej": j.label}}
	res := j.tg.apiCall(http.MethodPost, "/v1/sandboxes", body, j.key)
	if res.err != nil {
		t.Fatalf("create desktop: %v", res.err)
	}
	switch code := problemCode(res.body); {
	case code == "sandbox_unsupported":
		t.Skipf("sandbox desktops are switched off on this installation: %s", tail(string(res.body), 300))
	case code == "agent_limit" && strings.Contains(string(res.body), "SandboxDesktopEnabled"):
		t.Skipf("this account's plan has no desktops: %s", tail(string(res.body), 300))
	}
	if res.status/100 != 2 {
		j.skipUnavailable(t, res)
		t.Fatalf("create desktop: %s", j.tg.redact(res.String()))
	}
	var v struct {
		ID         string `json:"id"`
		Resolution string `json:"resolution"`
		RAMMB      int    `json:"ram_mb"`
	}
	if err := json.Unmarshal(res.body, &v); err != nil || v.ID == "" {
		t.Fatalf("create desktop answered no id: %s", tail(string(res.body), 500))
	}
	t.Cleanup(func() { j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+v.ID, nil, j.key) })
	if v.Resolution != "1024x768" || v.RAMMB < 2048 {
		t.Errorf("desktop view: resolution %q, ram_mb %d; want 1024x768 and at least 2048", v.Resolution, v.RAMMB)
	}
	token := j.waitReady(t, v.ID)

	gd := func(args ...string) string {
		t.Helper()
		res := j.data(http.MethodPost, v.ID, token, "/exec", nil, map[string]any{
			"cmd": append([]string{"/usr/local/bin/gregale-desktop"}, args...), "env": map[string]string{"DISPLAY": ":0"}, "timeout_ms": 60000,
		})
		var out sandboxExec
		if res.err != nil || res.status != http.StatusOK || json.Unmarshal(res.body, &out) != nil || out.ExitCode != 0 {
			t.Fatalf("gregale-desktop %s: %s", strings.Join(args, " "), j.tg.redact(res.String()))
		}
		return out.Stdout
	}
	if got := strings.TrimSpace(gd("size")); got != "1024x768" {
		t.Errorf("screen size = %q, want 1024x768", got)
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimSpace(gd("screenshot", "--base64")))
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || len(png) < 4096 {
		t.Errorf("screenshot: %d bytes, %v; want a PNG of the desktop", len(png), err)
	}
	gd("move", "100", "200")
	if got := strings.TrimSpace(gd("cursor")); got != "100 200" {
		t.Errorf("cursor after a move to 100 200 = %q", got)
	}

	// The live view: noVNC on 6080, view-only on 6081, each behind its own
	// preview token.
	views := map[int]string{}
	for _, port := range []int{6080, 6081} {
		res := j.tg.apiCall(http.MethodGet, "/v1/sandboxes/"+v.ID+"/ports/"+strconv.Itoa(port), nil, j.key)
		if problemCode(res.body) == "sandbox_previews_off" {
			t.Logf("preview URLs are off on this installation; the live view is not checked")
			return
		}
		var pv struct {
			URL          string `json:"url"`
			PreviewToken string `json:"preview_token"`
		}
		if res.err != nil || res.status != http.StatusOK || json.Unmarshal(res.body, &pv) != nil || pv.PreviewToken == "" {
			t.Fatalf("desktop view port %d: %s", port, j.tg.redact(res.String()))
		}
		j.tg.addSensitive(pv.PreviewToken)
		views[port] = pv.URL + "\x00" + pv.PreviewToken
	}
	get := func(view, token string) (int, string) {
		url, _, _ := strings.Cut(view, "\x00")
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return resp.StatusCode, string(b)
	}
	tokenOf := func(view string) string { _, tok, _ := strings.Cut(view, "\x00"); return tok }
	for port, view := range views {
		if status, page := get(view, tokenOf(view)); status != http.StatusOK || !strings.Contains(page, "vnc.html") {
			t.Errorf("view %d with its token = %d %q, want the noVNC page", port, status, tail(page, 200))
		}
	}
	if status, _ := get(views[6080], tokenOf(views[6081])); status != http.StatusNotFound {
		t.Errorf("the interactive view with the view-only token = %d, want 404", status)
	}
}
