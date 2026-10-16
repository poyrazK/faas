package issueslack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var responseWorkspace = regexp.MustCompile(`^T[A-Z0-9]{8,31}$`)
var interactionPathPart = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidInteractionResponseURL(raw, workspace string) bool {
	if len(raw) > api.IssueSlackResponseURLMaxBytes || !responseWorkspace.MatchString(workspace) || strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "hooks.slack.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
		return false
	}
	parts := strings.Split(u.Path, "/")
	return len(parts) == 5 && parts[0] == "" && parts[1] == "actions" && parts[2] == workspace && interactionPathPart.MatchString(parts[3]) && interactionPathPart.MatchString(parts[4])
}
func (c *Client) RespondToInteraction(ctx context.Context, responseURL, workspace, text string) error {
	if !ValidInteractionResponseURL(responseURL, workspace) {
		return state.ErrInvalidArgument
	}
	body, err := json.Marshal(map[string]any{"response_type": "ephemeral", "replace_original": false, "text": text})
	if err != nil {
		return err
	}
	// Only the validated path comes from Slack's signed payload. Construct the
	// authority locally so request data cannot select a network destination.
	destination, err := url.Parse(responseURL)
	if err != nil {
		return state.ErrInvalidArgument
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://hooks.slack.com", bytes.NewReader(body))
	if err != nil {
		return state.ErrInvalidArgument
	}
	req.URL.Path = destination.Path
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return state.ErrInvalidArgument
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, api.IssueSlackResponseMaxBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return state.ErrInvalidArgument
	}
	return nil
}
