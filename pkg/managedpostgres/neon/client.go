package neon

import (
	"bytes"
	"context"
	"encoding/json"
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

func (p *Provider) doJSON(ctx context.Context, method, path string, query url.Values, input, output any, accepted ...int) (err error) {
	if p == nil || p.baseURL == nil || p.httpClient == nil || p.apiKey == "" || !strings.HasPrefix(path, "/") {
		return managedpostgres.ErrUnavailable
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
	endpoint := *p.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()

	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return managedpostgres.ErrInvalid
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return managedpostgres.ErrInvalid
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()

	if !acceptedStatus(response.StatusCode, accepted) {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return statusError(response.StatusCode)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil || len(payload) > maximumResponseBytes {
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
