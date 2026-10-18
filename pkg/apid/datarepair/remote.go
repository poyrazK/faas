package datarepair

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
)

// Executor is a trusted, application-bound data-plane service. The management
// API owns intent and approval; it never accepts evidence from its callers.
type Executor interface {
	Prepare(context.Context, core.Scope, string, string) (core.Plan, error)
	Get(context.Context, core.Scope, string) (core.Plan, error)
	Rehearse(context.Context, core.Scope, string, string) (core.Plan, error)
	Approve(context.Context, core.Scope, string, string, string) (core.Plan, error)
	Apply(context.Context, core.Scope, string, string, string, string) (core.Plan, error)
}

type Remote struct {
	origin, token string
	client        *http.Client
}

// NewRemote allows HTTPS and local development loopback. It follows no
// redirects, sends no database credentials, and retries no mutations.
func NewRemote(origin, token string) (*Remote, error) {
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(token, "\r\n") || len(token) < 32 {
		return nil, core.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return nil, core.ErrInvalid
	}
	return &Remote{origin: strings.TrimSuffix(origin, "/"), token: token, client: &http.Client{Timeout: time.Duration(api.DataRepairTimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Remote) call(ctx context.Context, s core.Scope, method, path string, body any) (core.Plan, error) {
	if s.Validate() != nil {
		return core.Plan{}, core.ErrInvalid
	}
	b, err := json.Marshal(body)
	if err != nil {
		return core.Plan{}, core.ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, method, c.origin+"/v1/data-repair/plans"+path, bytes.NewReader(b))
	if err != nil {
		return core.Plan{}, core.ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(r)
	if err != nil {
		return core.Plan{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 400:
			return core.Plan{}, core.ErrInvalid
		case 404:
			return core.Plan{}, core.ErrNotFound
		case 409:
			var p struct {
				Code string `json:"code"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, int64(api.DataRepairMaxRequestBytes))).Decode(&p) == nil && p.Code == "repair_check_failed" {
				return core.Plan{}, &core.CheckError{}
			}
			return core.Plan{}, core.ErrConflict
		case http.StatusGone:
			return core.Plan{}, core.ErrPurged
		}
		return core.Plan{}, core.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(api.DataRepairMaxPlanBytes+1)))
	if err != nil || len(raw) > api.DataRepairMaxPlanBytes {
		return core.Plan{}, core.ErrUnavailable
	}
	var p core.Plan
	if json.Unmarshal(raw, &p) != nil || p.Scope != s {
		return core.Plan{}, core.ErrUnavailable
	}
	return p, nil
}

func (c *Remote) Prepare(ctx context.Context, s core.Scope, source, id string) (core.Plan, error) {
	return c.call(ctx, s, "POST", "", request{OperationID: source, PlanID: id})
}
func (c *Remote) Get(ctx context.Context, s core.Scope, id string) (core.Plan, error) {
	return c.call(ctx, s, "GET", "/"+id, nil)
}
func (c *Remote) Rehearse(ctx context.Context, s core.Scope, id, digest string) (core.Plan, error) {
	return c.call(ctx, s, "POST", "/"+id+"/rehearse", request{Digest: digest})
}
func (c *Remote) Approve(ctx context.Context, s core.Scope, id, digest, actor string) (core.Plan, error) {
	return c.call(ctx, s, "POST", "/"+id+"/approve", request{Digest: digest, Actor: actor})
}
func (c *Remote) Apply(ctx context.Context, s core.Scope, id, digest, repairID, _ string) (core.Plan, error) {
	return c.call(ctx, s, "POST", "/"+id+"/apply", request{Digest: digest, RepairOperationID: repairID})
}
