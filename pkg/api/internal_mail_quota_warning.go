package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const InternalMailQuotaWarningTokenPathEnv = "FAAS_INTERNAL_MAIL_QUOTA_WARNING_TOKEN_PATH"

type InternalMailQuotaWarningRequest struct {
	Day       time.Time `json:"day"`
	Plan      Plan      `json:"plan"`
	RequestID string    `json:"request_id"`
}

type InternalMailQuotaWarningResponse struct {
	IntentID      string    `json:"intent_id,omitempty"`
	LeaseID       string    `json:"lease_id,omitempty"`
	Day           time.Time `json:"day"`
	ObservedAt    time.Time `json:"observed_at"`
	Plan          Plan      `json:"plan,omitempty"`
	UsedMBSeconds int64     `json:"used_mb_seconds"`
	QuotaGB       int       `json:"quota_gb"`
	Followup      bool      `json:"followup"`
}

type InternalMailQuotaWarningCompleteRequest struct {
	IntentID string `json:"intent_id"`
	LeaseID  string `json:"lease_id"`
}

// InternalMailQuotaWarningUsageGBHours preserves meterd's existing six-decimal
// rounding at the quota decision/presentation seam. Physical usage stays integer.
func InternalMailQuotaWarningUsageGBHours(mbSeconds int64) float64 {
	gb := float64(mbSeconds) / 1024.0 / 3600.0
	return float64(int64(gb*1e6+0.5)) / 1e6
}

// LoadInternalMailQuotaWarningToken reads only an explicitly configured private
// service credential. Neither daemon calls this with the own flag disabled.
func LoadInternalMailQuotaWarningToken(path string) (string, error) {
	f, err := os.Open(path) //nolint:forbidigo // Explicit operator-provisioned service credential, never a customer path; the read below is byte-bounded.
	if err != nil {
		return "", errors.New("quota warning command credential unavailable")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, InternalMailQuotaWarningTokenMaxBytes+1))
	defer clear(raw)
	if err != nil || len(raw) > InternalMailQuotaWarningTokenMaxBytes {
		return "", errors.New("quota warning command credential invalid")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < InternalMailQuotaWarningTokenMinBytes || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return "", errors.New("quota warning command credential invalid")
	}
	return token, nil
}

// InternalMailQuotaWarningClient only addresses apid's existing loopback operator
// listener. It has no generic mail-send capability, redirects, proxy or SDK route.
type InternalMailQuotaWarningClient struct {
	origin, token string
	http          *http.Client
}

func NewInternalMailQuotaWarningClient(origin, token string) (*InternalMailQuotaWarningClient, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("quota warning command requires a plain loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 || len(token) < InternalMailQuotaWarningTokenMinBytes || len(token) > InternalMailQuotaWarningTokenMaxBytes || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return nil, errors.New("quota warning command origin or credential invalid")
	}
	return &InternalMailQuotaWarningClient{origin: origin, token: token, http: &http.Client{
		Timeout:       InternalMailQuotaWarningCommandTimeout,
		Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: InternalMailQuotaWarningCommandTimeout}).DialContext, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Minute, ResponseHeaderTimeout: InternalMailQuotaWarningCommandTimeout},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *InternalMailQuotaWarningClient) Probe(ctx context.Context) error {
	return c.command(ctx, http.MethodGet, "/v1/internal/billing/quota-warnings/readyz", nil, nil)
}

func (c *InternalMailQuotaWarningClient) Warn(ctx context.Context, id string, day time.Time, plan Plan) (InternalMailQuotaWarningResponse, error) {
	var out InternalMailQuotaWarningResponse
	if !canonicalQuotaWarningID(id) || day.IsZero() || !day.Equal(day.UTC().Truncate(24*time.Hour)) || !quotaWarningPaidPlan(plan) {
		return out, errors.New("invalid quota warning command")
	}
	err := c.command(ctx, http.MethodPost, "/v1/internal/billing/accounts/"+id+"/quota-warning", InternalMailQuotaWarningRequest{Day: day, Plan: plan, RequestID: uuid.NewString()}, &out)
	if err == nil && out.Followup && (!canonicalQuotaWarningID(out.IntentID) || !canonicalQuotaWarningID(out.LeaseID) || !out.Day.Equal(day) || out.Plan != plan || out.ObservedAt.Before(day) || !out.ObservedAt.Before(day.Add(24*time.Hour)) || out.UsedMBSeconds < 0 || out.QuotaGB < 1 || InternalMailQuotaWarningUsageGBHours(out.UsedMBSeconds) < float64(out.QuotaGB)) {
		return InternalMailQuotaWarningResponse{}, errors.New("invalid quota warning response")
	}
	return out, err
}

func quotaWarningPaidPlan(plan Plan) bool {
	return plan == PlanHobby || plan == PlanPro || plan == PlanScale
}

func (c *InternalMailQuotaWarningClient) Complete(ctx context.Context, id, intentID, leaseID string) error {
	if !canonicalQuotaWarningID(id) || !canonicalQuotaWarningID(intentID) || !canonicalQuotaWarningID(leaseID) {
		return errors.New("invalid quota warning completion")
	}
	return c.command(ctx, http.MethodPost, "/v1/internal/billing/accounts/"+id+"/quota-warning/complete", InternalMailQuotaWarningCompleteRequest{IntentID: intentID, LeaseID: leaseID}, nil)
}

func canonicalQuotaWarningID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func (c *InternalMailQuotaWarningClient) command(ctx context.Context, method, path string, input, output any) error {
	if c == nil || c.http == nil {
		return errors.New("quota warning command unavailable")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("invalid quota warning command body")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("quota warning command unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	want := http.StatusNoContent
	if output != nil {
		want = http.StatusOK
	}
	if resp.StatusCode != want {
		return fmt.Errorf("quota warning command returned HTTP %d", resp.StatusCode)
	}
	if output == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, InternalMailQuotaWarningCommandMaxBytes+1))
	if err != nil || len(raw) > InternalMailQuotaWarningCommandMaxBytes {
		return errors.New("quota warning command response unavailable")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(output) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid quota warning command response")
	}
	return nil
}
