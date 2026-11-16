package neon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/trace"
)

const maximumResponseBytes = 4 << 20

// brokerForwarder sends one Neon call through neond (ADR-20261112160000),
// which holds the key.
type brokerForwarder interface {
	Forward(ctx context.Context, method, path, rawQuery string, body []byte) (status int, reply []byte, retryAfter string, err error)
}

func (p *Provider) doJSON(ctx context.Context, method, path string, query url.Values, input, output any, accepted ...int) (err error) {
	if p == nil || !strings.HasPrefix(path, "/") || (p.broker == nil && (p.baseURL == nil || p.httpClient == nil || p.apiKey == "")) {
		return managedpostgres.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.requestsDeferred(path) {
		return managedpostgres.ErrUnavailable
	}
	if managedpostgres.BackgroundRequests(ctx) {
		if err := p.paceBackground(ctx); err != nil {
			return err
		}
	}
	ctx, span := trace.StartSpan(ctx, "gregale.binding.managed_postgres",
		attribute.String("gregale.dependency.type", "managed_binding"),
		attribute.String("gregale.binding.type", "managed_postgres"),
		attribute.String("gregale.binding.provider", "neon"),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "managed binding request failed")
		}
		span.End()
	}()
	var encoded []byte
	if input != nil {
		if encoded, err = json.Marshal(input); err != nil {
			return managedpostgres.ErrInvalid
		}
	}
	var (
		status     int
		payload    []byte
		retryAfter string
	)
	if p.broker != nil {
		status, payload, retryAfter, err = p.broker.Forward(ctx, method, path, query.Encode(), encoded)
	} else {
		status, payload, retryAfter, err = p.callNeon(ctx, method, path, query, encoded)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, managedpostgres.ErrInvalid) {
			return err
		}
		return managedpostgres.ErrUnavailable
	}
	if !acceptedStatus(status, accepted) {
		if status == http.StatusTooManyRequests {
			p.deferRequests(path, retryAfter)
		}
		return refusedRequestError(method, path, status, payload)
	}
	if output == nil || status == http.StatusNoContent {
		return nil
	}
	if len(payload) > maximumResponseBytes {
		return managedpostgres.ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(output); err != nil {
		return managedpostgres.ErrUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

// callNeon sends one request to Neon with the backend's own key and reads at
// most maximumResponseBytes+1 of the reply.
func (p *Provider) callNeon(ctx context.Context, method, path string, query url.Values, encoded []byte) (int, []byte, string, error) {
	endpoint := *p.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	var body io.Reader
	if encoded != nil {
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return 0, nil, "", managedpostgres.ErrInvalid
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	if encoded != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// New pins the authority to apiBaseURL and rejects redirects. Caller data
	// changes only URL.Path and RawQuery; it cannot select a different origin.
	response, err := p.httpClient.Do(request)
	if err != nil {
		return 0, nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return 0, nil, "", err
	}
	return response.StatusCode, payload, response.Header.Get("Retry-After"), nil
}

func acceptedStatus(status int, accepted []int) bool {
	for _, candidate := range accepted {
		if status == candidate {
			return true
		}
	}
	return false
}

func statusError(status int) error {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return managedpostgres.ErrInvalid
	case http.StatusNotFound:
		return managedpostgres.ErrNotFound
	case http.StatusConflict:
		return managedpostgres.ErrConflict
	case http.StatusTooManyRequests, http.StatusLocked:
		return managedpostgres.ErrUnavailable
	default:
		return managedpostgres.ErrUnavailable
	}
}
