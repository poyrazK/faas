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

const InternalMailNonpaymentDeletionTokenPathEnv = "FAAS_INTERNAL_MAIL_NONPAYMENT_DELETION_TOKEN_PATH"

type InternalMailNonpaymentDeletionRequest struct {
	PastDueAt time.Time `json:"past_due_at"`
	RequestID string    `json:"request_id"`
}

type InternalMailNonpaymentDeletionResponse struct {
	IntentID            string    `json:"intent_id,omitempty"`
	LeaseID             string    `json:"lease_id,omitempty"`
	DeletionRequestedAt time.Time `json:"deletion_requested_at"`
	Followup            bool      `json:"followup"`
}

type InternalMailNonpaymentDeletionCompleteRequest struct {
	IntentID string `json:"intent_id"`
	LeaseID  string `json:"lease_id"`
}

// LoadInternalMailNonpaymentDeletionToken reads only an explicitly configured private
// service credential. Neither daemon calls this with the own flag disabled.
func LoadInternalMailNonpaymentDeletionToken(path string) (string, error) {
	f, err := os.Open(path) //nolint:forbidigo // Explicit operator-provisioned service credential, never a customer path; the read below is byte-bounded.
	if err != nil {
		return "", errors.New("non-payment deletion command credential unavailable")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, InternalMailNonpaymentDeletionTokenMaxBytes+1))
	defer clear(raw)
	if err != nil || len(raw) > InternalMailNonpaymentDeletionTokenMaxBytes {
		return "", errors.New("non-payment deletion command credential invalid")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < InternalMailNonpaymentDeletionTokenMinBytes || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return "", errors.New("non-payment deletion command credential invalid")
	}
	return token, nil
}

// InternalMailNonpaymentDeletionClient only addresses apid's existing loopback operator
// listener. It has no generic mail-send capability, redirects, proxy or SDK route.
type InternalMailNonpaymentDeletionClient struct {
	origin, token string
	http          *http.Client
}

func NewInternalMailNonpaymentDeletionClient(origin, token string) (*InternalMailNonpaymentDeletionClient, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("non-payment deletion command requires a plain loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 || len(token) < InternalMailNonpaymentDeletionTokenMinBytes || len(token) > InternalMailNonpaymentDeletionTokenMaxBytes || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return nil, errors.New("non-payment deletion command origin or credential invalid")
	}
	return &InternalMailNonpaymentDeletionClient{origin: origin, token: token, http: &http.Client{
		Timeout:       InternalMailNonpaymentDeletionCommandTimeout,
		Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: InternalMailNonpaymentDeletionCommandTimeout}).DialContext, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Minute, ResponseHeaderTimeout: InternalMailNonpaymentDeletionCommandTimeout},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *InternalMailNonpaymentDeletionClient) Probe(ctx context.Context) error {
	return c.command(ctx, http.MethodGet, "/v1/internal/billing/nonpayment-deletions/readyz", nil, nil)
}

func (c *InternalMailNonpaymentDeletionClient) Schedule(ctx context.Context, id string, pastDueAt time.Time) (InternalMailNonpaymentDeletionResponse, error) {
	var out InternalMailNonpaymentDeletionResponse
	if !canonicalNonpaymentDeletionID(id) || pastDueAt.IsZero() {
		return out, errors.New("invalid non-payment deletion command")
	}
	err := c.command(ctx, http.MethodPost, "/v1/internal/billing/accounts/"+id+"/nonpayment-deletion", InternalMailNonpaymentDeletionRequest{PastDueAt: pastDueAt, RequestID: uuid.NewString()}, &out)
	if err == nil && out.Followup && (!canonicalNonpaymentDeletionID(out.IntentID) || !canonicalNonpaymentDeletionID(out.LeaseID) || out.DeletionRequestedAt.IsZero()) {
		return InternalMailNonpaymentDeletionResponse{}, errors.New("invalid non-payment deletion command response")
	}
	return out, err
}

func (c *InternalMailNonpaymentDeletionClient) Complete(ctx context.Context, id, intentID, leaseID string) error {
	if !canonicalNonpaymentDeletionID(id) || !canonicalNonpaymentDeletionID(intentID) || !canonicalNonpaymentDeletionID(leaseID) {
		return errors.New("invalid non-payment deletion completion")
	}
	return c.command(ctx, http.MethodPost, "/v1/internal/billing/accounts/"+id+"/nonpayment-deletion/complete", InternalMailNonpaymentDeletionCompleteRequest{IntentID: intentID, LeaseID: leaseID}, nil)
}

func canonicalNonpaymentDeletionID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func (c *InternalMailNonpaymentDeletionClient) command(ctx context.Context, method, path string, input, output any) error {
	if c == nil || c.http == nil {
		return errors.New("non-payment deletion command unavailable")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("invalid non-payment deletion command body")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("non-payment deletion command unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	want := http.StatusNoContent
	if output != nil {
		want = http.StatusOK
	}
	if resp.StatusCode != want {
		return fmt.Errorf("non-payment deletion command returned HTTP %d", resp.StatusCode)
	}
	if output == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, InternalMailNonpaymentDeletionCommandMaxBytes+1))
	if err != nil || len(raw) > InternalMailNonpaymentDeletionCommandMaxBytes {
		return errors.New("non-payment deletion command response unavailable")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(output) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid non-payment deletion command response")
	}
	return nil
}
