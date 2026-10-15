package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const lineageRetryBundleFormat = "gregale.event-lineage-retry"
const lineageRetryBundleVersion = 1

type lineageRetryFileScope struct {
	AppID          string `json:"app_id"`
	SubscriptionID string `json:"subscription_id"`
}

type lineageRetryFileSource struct {
	RequestID string    `json:"request_id"`
	CreatedAt time.Time `json:"created_at"`
}

type lineageRetryBundle struct {
	Format         string                                         `json:"format"`
	Version        int                                            `json:"version"`
	AppID          string                                         `json:"app_id"`
	SubscriptionID string                                         `json:"subscription_id"`
	Request        api.CreateEventExecutionLineageRetryJobRequest `json:"request"`
	SourceJob      *lineageRetryFileSource                        `json:"source_job,omitempty"`
}

type lineageRetryRequestFile struct {
	Request   api.CreateEventExecutionLineageRetryJobRequest
	Scope     *lineageRetryFileScope
	SourceJob *lineageRetryFileSource
}

func readLineageRetryRequestFile(path string) (lineageRetryRequestFile, error) {
	var out lineageRetryRequestFile
	if path == "" {
		return out, fmt.Errorf("--file is required")
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.EventExecutionLineageRetryJobBodyMaxBytes+1))
	if err != nil {
		return out, err
	}
	if len(raw) > api.EventExecutionLineageRetryJobBodyMaxBytes {
		return out, fmt.Errorf("request file exceeds %d bytes", api.EventExecutionLineageRetryJobBodyMaxBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return out, err
	}
	scoped := false
	for key := range fields {
		switch strings.ToLower(key) {
		case "format", "version", "app_id", "subscription_id", "request", "source_job":
			scoped = true
		}
	}
	if !scoped {
		if err := json.Unmarshal(raw, &out.Request); err != nil {
			return out, err
		}
		out.Request, err = normalizeLineagePreflightRequest(out.Request)
		return out, err
	}
	if err := rejectDuplicateLineageJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return out, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var bundle lineageRetryBundle
	if err := decoder.Decode(&bundle); err != nil {
		return out, fmt.Errorf("invalid scoped retry file: %w", err)
	}
	if err := normalizeLineageRetryBundle(&bundle); err != nil {
		return out, err
	}
	out.Request, out.Scope, out.SourceJob = bundle.Request, &lineageRetryFileScope{AppID: bundle.AppID, SubscriptionID: bundle.SubscriptionID}, bundle.SourceJob
	return out, nil
}

// Duplicate or case-aliased fields make a reviewed bundle ambiguous. Reject them at every level.
func rejectDuplicateLineageJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return fmt.Errorf("invalid scoped retry object key")
			}
			key = strings.ToLower(key)
			if keys[key] {
				return fmt.Errorf("duplicate scoped retry field %q", key)
			}
			keys[key] = true
		}
		if err := rejectDuplicateLineageJSON(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func normalizeLineageRetryBundle(bundle *lineageRetryBundle) error {
	if bundle.Format != lineageRetryBundleFormat || bundle.Version != lineageRetryBundleVersion {
		return fmt.Errorf("unsupported scoped retry file format or version")
	}
	app, appErr := uuid.Parse(bundle.AppID)
	sub, subErr := uuid.Parse(bundle.SubscriptionID)
	if appErr != nil || subErr != nil || app == uuid.Nil || sub == uuid.Nil {
		return fmt.Errorf("scoped retry file requires nonzero app and subscription UUIDs")
	}
	req, err := normalizeLineagePreflightRequest(bundle.Request)
	if err != nil {
		return err
	}
	bundle.AppID, bundle.SubscriptionID, bundle.Request = app.String(), sub.String(), req
	if source := bundle.SourceJob; source != nil {
		id, err := uuid.Parse(source.RequestID)
		if err != nil || id == uuid.Nil || source.CreatedAt.IsZero() {
			return fmt.Errorf("source provenance requires a nonzero UUID and original creation timestamp")
		}
		source.RequestID, source.CreatedAt = id.String(), source.CreatedAt.UTC()
	}
	return nil
}

func (file lineageRetryRequestFile) validateSubscription(sub string) error {
	if file.Scope == nil {
		return nil
	}
	id, err := uuid.Parse(sub)
	if err != nil || id == uuid.Nil || id.String() != file.Scope.SubscriptionID {
		return fmt.Errorf("retry file targets subscription %s; supplied subscription does not match", file.Scope.SubscriptionID)
	}
	return nil
}

func (file lineageRetryRequestFile) validateApp(ctx context.Context, client *api.Client, app string) error {
	if file.Scope == nil {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	target, err := client.GetApp(readCtx, app)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(target.ID)
	if err != nil || id == uuid.Nil || id.String() != file.Scope.AppID {
		return fmt.Errorf("retry file targets app %s; supplied app does not match", file.Scope.AppID)
	}
	return nil
}

func writeScopedLineageRetryExport(path string, req api.CreateEventExecutionLineageRetryJobRequest, app, sub string, source *lineageRetryFileSource) error {
	bundle := lineageRetryBundle{Format: lineageRetryBundleFormat, Version: lineageRetryBundleVersion, AppID: app, SubscriptionID: sub, Request: req, SourceJob: source}
	if err := normalizeLineageRetryBundle(&bundle); err != nil {
		return err
	}
	return writeLineageRetryFile(path, bundle)
}
