// spec: §14.A
// adr: 1527
package m9

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type drainStatus struct {
	NodeName    string     `json:"node_name"`
	Lifecycle   string     `json:"lifecycle"`
	InitiatedAt *time.Time `json:"drain_initiated_at,omitempty"`
	CompletedAt *time.Time `json:"drain_completed_at,omitempty"`
}

func (s drainStatus) pending(previousCompletion *time.Time) bool {
	if s.Lifecycle != "draining" || s.InitiatedAt == nil {
		return false
	}
	return s.CompletedAt == nil && previousCompletion == nil ||
		s.CompletedAt != nil && previousCompletion != nil && s.CompletedAt.Equal(*previousCompletion)
}

type drainAPI struct {
	origin string
	token  string
	client *http.Client
}

func newDrainAPI(config DrainConfig, token string) (*drainAPI, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("M9 drain requires FAAS_M9_OPERATOR_TOKEN from an allowlisted operator")
	}
	return &drainAPI{origin: strings.TrimRight(config.APIURL, "/"), token: token,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}, nil
}

func (a *drainAPI) call(ctx context.Context, method, path string, out any, allowed ...int) error {
	req, err := http.NewRequestWithContext(ctx, method, a.origin+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		// Do not propagate private response bodies or request headers into evidence.
		return errors.New("M9 drain API transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	ok := resp.StatusCode == http.StatusOK || method == http.MethodPost && resp.StatusCode == http.StatusAccepted
	for _, code := range allowed {
		ok = ok || resp.StatusCode == code
	}
	if !ok {
		return fmt.Errorf("M9 drain API returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, out) != nil {
		return errors.New("M9 drain API returned an incomplete or invalid JSON response")
	}
	return nil
}

func (a *drainAPI) preflight(ctx context.Context, name, id string) error {
	var node struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	path := "/v1/compute-nodes/" + url.PathEscape(name)
	if err := a.call(ctx, http.MethodGet, path, &node); err != nil {
		return err
	}
	if node.ID != id || node.Name != name {
		return errors.New("M9 API and database resolve different compute nodes")
	}
	var progress struct {
		NodeName  string `json:"node_name"`
		Lifecycle string `json:"lifecycle"`
		Code      string `json:"code"`
	}
	if err := a.call(ctx, http.MethodGet, path+"/drain", &progress, http.StatusNotFound); err != nil {
		return err
	}
	if progress.Code == "not_found" {
		return nil
	} // Authenticated operator, node never drained.
	if progress.NodeName != name || progress.Lifecycle != "active" {
		return errors.New("M9 drain preflight requires authorized progress for the active source")
	}
	return nil
}

func (a *drainAPI) status(ctx context.Context, method, name string) (drainStatus, error) {
	var result drainStatus
	err := a.call(ctx, method, "/v1/compute-nodes/"+url.PathEscape(name)+"/drain", &result)
	if err == nil && (result.NodeName != name || result.Lifecycle == "" || result.InitiatedAt == nil) {
		err = errors.New("M9 drain API returned unrelated or incomplete progress")
	}
	return result, err
}
