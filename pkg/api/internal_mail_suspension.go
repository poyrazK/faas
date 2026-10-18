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

const InternalMailSuspensionTokenPathEnv = "FAAS_INTERNAL_MAIL_SUSPENSION_TOKEN_PATH"

type InternalMailSuspensionRequest struct {
	PastDueAt time.Time `json:"past_due_at"`
	RequestID string    `json:"request_id"`
}

type InternalMailSuspensionResponse struct {
	IntentID    string    `json:"intent_id,omitempty"`
	LeaseID     string    `json:"lease_id,omitempty"`
	SuspendedAt time.Time `json:"suspended_at"`
	Followup    bool      `json:"followup"`
}

type InternalMailSuspensionCompleteRequest struct {
	IntentID string `json:"intent_id"`
	LeaseID  string `json:"lease_id"`
}

// LoadInternalMailSuspensionToken reads only an explicitly configured private
// service credential. Neither daemon calls this with the own flag disabled.
func LoadInternalMailSuspensionToken(path string) (string, error) {
	f, err := os.Open(path) //nolint:forbidigo // Explicit operator-provisioned service credential, never a customer path; the read below is byte-bounded.
	if err != nil {
		return "", errors.New("suspension command credential unavailable")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, InternalMailSuspensionTokenMaxBytes+1))
	defer clear(raw)
	if err != nil || len(raw) > InternalMailSuspensionTokenMaxBytes {
		return "", errors.New("suspension command credential invalid")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < InternalMailSuspensionTokenMinBytes || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return "", errors.New("suspension command credential invalid")
	}
	return token, nil
}

// InternalMailSuspensionClient only addresses apid's existing loopback operator
// listener. It has no generic mail-send capability, redirects, proxy or SDK route.
type InternalMailSuspensionClient struct {
	origin, token string
	http          *http.Client
}

func NewInternalMailSuspensionClient(origin, token string) (*InternalMailSuspensionClient, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("suspension command requires a plain loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 || len(token) < InternalMailSuspensionTokenMinBytes || len(token) > InternalMailSuspensionTokenMaxBytes || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return nil, errors.New("suspension command origin or credential invalid")
	}
	return &InternalMailSuspensionClient{origin: origin, token: token, http: &http.Client{
		Timeout:       InternalMailSuspensionCommandTimeout,
		Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: InternalMailSuspensionCommandTimeout}).DialContext, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Minute, ResponseHeaderTimeout: InternalMailSuspensionCommandTimeout},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *InternalMailSuspensionClient) Probe(ctx context.Context) error {
	return c.command(ctx, http.MethodGet, "/v1/internal/billing/suspensions/readyz", nil, nil)
}

func (c *InternalMailSuspensionClient) Suspend(ctx context.Context, id string, pastDueAt time.Time) (InternalMailSuspensionResponse, error) {
	var out InternalMailSuspensionResponse
	if !canonicalSuspensionID(id) || pastDueAt.IsZero() {
		return out, errors.New("invalid suspension command")
	}
	err := c.command(ctx, http.MethodPost, "/v1/internal/billing/accounts/"+id+"/suspension", InternalMailSuspensionRequest{PastDueAt: pastDueAt, RequestID: uuid.NewString()}, &out)
	if err == nil && out.Followup && (!canonicalSuspensionID(out.IntentID) || !canonicalSuspensionID(out.LeaseID) || out.SuspendedAt.IsZero()) {
		return InternalMailSuspensionResponse{}, errors.New("invalid suspension command response")
	}
	return out, err
}

func (c *InternalMailSuspensionClient) Complete(ctx context.Context, id, intentID, leaseID string) error {
	if !canonicalSuspensionID(id) || !canonicalSuspensionID(intentID) || !canonicalSuspensionID(leaseID) {
		return errors.New("invalid suspension completion")
	}
	return c.command(ctx, http.MethodPost, "/v1/internal/billing/accounts/"+id+"/suspension/complete", InternalMailSuspensionCompleteRequest{IntentID: intentID, LeaseID: leaseID}, nil)
}

func canonicalSuspensionID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func (c *InternalMailSuspensionClient) command(ctx context.Context, method, path string, input, output any) error {
	if c == nil || c.http == nil {
		return errors.New("suspension command unavailable")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("invalid suspension command body")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("suspension command unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	want := http.StatusNoContent
	if output != nil {
		want = http.StatusOK
	}
	if resp.StatusCode != want {
		return fmt.Errorf("suspension command returned HTTP %d", resp.StatusCode)
	}
	if output == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, InternalMailSuspensionCommandMaxBytes+1))
	if err != nil || len(raw) > InternalMailSuspensionCommandMaxBytes {
		return errors.New("suspension command response unavailable")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(output) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid suspension command response")
	}
	return nil
}
