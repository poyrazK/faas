package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const recoverActivityConsumerTool = "gregale_recover_environment_cache_activity_consumer"

type activityConsumerRecoveryArgs struct {
	App           string `json:"app" jsonschema:"Gregale app slug"`
	Project       string `json:"project" jsonschema:"Owning project"`
	Environment   string `json:"environment" jsonschema:"Named environment with current read consent"`
	Consumer      string `json:"consumer" jsonschema:"Durable consumer name, maximum 64 bytes"`
	RecoveryToken string `json:"recovery_token,omitempty" jsonschema:"Opaque continuation from this consumer recovery; omit to start a new complete baseline"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Schedules per baseline page, default 20 and maximum 50"`
}

// Untrusted pagination metadata, not a grant or proof of processing. Embed the
// raw continuation structure to avoid double base64 expansion within its bound.
type activityConsumerRecoveryToken struct {
	Version    int                              `json:"v"`
	Binding    string                           `json:"b"`
	Revision   int64                            `json:"r"`
	Generation string                           `json:"g"`
	Recovery   environmentScheduleRecoveryToken `json:"recovery"`
}

type activityConsumerRecoveryResult struct {
	environmentScheduleRecoveryResult
	Consumer       string                   `json:"consumer"`
	Revision       int64                    `json:"revision"`
	Generation     string                   `json:"generation"`
	Acknowledgment *activityConsumerAckArgs `json:"acknowledgment,omitempty"`
}

func (m *management) consumerRecoveryBinding(b activityConsumerBinding) string {
	data, _ := json.Marshal([]string{m.opts.planNamespace, b.key()})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func decodeConsumerRecoveryToken(raw string) (activityConsumerRecoveryToken, error) {
	var token activityConsumerRecoveryToken
	bad := invalid("invalid_argument", "invalid consumer recovery token; restart without recovery_token")
	if len(raw) > api.EnvironmentCacheActivityRecoveryTokenMaxBytes {
		return token, bad
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return token, bad
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&token) != nil || d.Decode(&struct{}{}) != io.EOF || token.Version != 1 || len(token.Binding) != 64 || token.Revision < 0 || token.Revision >= api.RouteRequirementsMaxRevision || token.Generation != "" && !consumerGeneration.MatchString(token.Generation) || token.Recovery.Version != 1 || token.Recovery.After == "" || token.Recovery.Checkpoint == "" {
		return token, bad
	}
	return token, nil
}

func validateConsumerRecoveryCursor(cursor string, b activityConsumerBinding, record activityConsumerRecord) error {
	point, err := api.ParseEnvironmentCacheActivityCursor(cursor, b.Account, b.App, b.Project, b.Environment)
	saved, _ := api.ParseEnvironmentCacheActivityCursor(record.Cursor, b.Account, b.App, b.Project, b.Environment)
	if cursor == "" || err != nil || point.Sequence < saved.Sequence {
		return invalid("invalid_argument", "recovery checkpoint must belong to this account/environment and cannot precede saved progress")
	}
	return nil
}

func validateConsumerRecoveryTarget(t environmentScheduleRecoveryToken, b activityConsumerBinding, record activityConsumerRecord) error {
	if t.Endpoint != b.Endpoint || t.Account != b.Account || t.App != b.App || t.Project != b.Project || t.Environment != b.Environment || t.Scope != b.Scope {
		return invalid("invalid_argument", "recovery token belongs to another endpoint, account or immutable environment")
	}
	return validateConsumerRecoveryCursor(t.Checkpoint, b, record)
}

func (m *management) prepareConsumerRecovery(ctx context.Context, args activityConsumerRecoveryArgs) (activityConsumerBinding, activityConsumerRecord, environmentScheduleRecoveryArgs, error) {
	var b activityConsumerBinding
	var record activityConsumerRecord
	raw := environmentScheduleRecoveryArgs{App: args.App, Project: args.Project, Environment: args.Environment, Limit: args.Limit}
	if _, err := api.NormalizeEnvironmentCacheScheduleList(api.EnvironmentCacheScheduleListOptions{Limit: args.Limit}); err != nil || len(args.RecoveryToken) > api.EnvironmentCacheActivityRecoveryTokenMaxBytes {
		return b, record, raw, invalid("invalid_argument", "select a bounded consumer recovery page")
	}
	var token activityConsumerRecoveryToken
	var err error
	if args.RecoveryToken != "" {
		token, err = decodeConsumerRecoveryToken(args.RecoveryToken)
		if err != nil {
			return b, record, raw, err
		}
	}
	base := activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer}
	b, _, err = m.activityConsumerTarget(ctx, base)
	if err != nil {
		return b, record, raw, err
	}
	record, err = m.consumerRecord(ctx, base, b, nil)
	if err != nil {
		return b, record, raw, err
	}
	if record.Revision >= api.RouteRequirementsMaxRevision {
		return b, record, raw, invalid("checkpoint_conflict", "consumer revision is exhausted; inspect and explicitly retire it before reinitializing progress")
	}
	if args.RecoveryToken != "" {
		if token.Binding != m.consumerRecoveryBinding(b) {
			return b, record, raw, invalid("invalid_argument", "recovery token belongs to another consumer or connection")
		}
		if token.Generation != record.Generation {
			return b, record, raw, consumerGenerationConflict()
		}
		if token.Revision != record.Revision {
			return b, record, raw, invalid("checkpoint_conflict", "consumer changed during recovery; discard partial results and restart without recovery_token")
		}
		if err = validateConsumerRecoveryTarget(token.Recovery, b, record); err != nil {
			return b, record, raw, err
		}
		data, _ := json.Marshal(token.Recovery)
		raw.RecoveryToken = base64.RawURLEncoding.EncodeToString(data)
	}
	return b, record, raw, nil
}

func (m *management) recoverActivityConsumer(ctx context.Context, args activityConsumerRecoveryArgs) (any, error) {
	b, record, raw, err := m.prepareConsumerRecovery(ctx, args)
	if err != nil {
		return nil, err
	}
	value, err := m.recoverEnvironmentCacheScheduleActivity(ctx, raw)
	base := activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer}
	// Even when native recovery fails, never release stale recovery metadata for
	// a consumer that was retired, changed or revoked during that native read.
	if checkErr := m.checkConsumerObservation(ctx, base, b, record); checkErr != nil {
		return nil, checkErr
	}
	if err != nil {
		var restart *activityRecoveryRestartError
		if errors.As(err, &restart) {
			if e := validateScheduleActivityCheckpoint(restart.expired.recovery, b.Account, b.App, b.Project, b.Environment, b.Scope); e != nil {
				return nil, e
			}
		}
		return nil, err
	}
	out, err := m.consumerRecoveryOutput(args, b, record, value.(environmentScheduleRecoveryResult))
	if err != nil {
		return nil, err
	}
	return guardConsumerRead(out, base, b, record), nil
}

func (m *management) consumerRecoveryOutput(args activityConsumerRecoveryArgs, b activityConsumerBinding, record activityConsumerRecord, page environmentScheduleRecoveryResult) (any, error) {
	if page.AppID != b.App || page.ProjectID != b.Project || page.EnvironmentID != b.Environment || page.Scope != b.Scope {
		return nil, invalid("invalid_response", "consumer recovery target changed during discovery")
	}
	out := activityConsumerRecoveryResult{environmentScheduleRecoveryResult: page, Consumer: b.Consumer, Revision: record.Revision, Generation: record.Generation}
	if page.Complete {
		if err := validateConsumerRecoveryCursor(page.ActivityCursor, b, record); err != nil {
			return nil, err
		}
		out.Acknowledgment = &activityConsumerAckArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer, Cursor: page.ActivityCursor, ExpectedRevision: record.Revision, ExpectedGeneration: record.Generation, BaselineRebuilt: record.Revision > 0}
	} else {
		inner, err := decodeScheduleRecoveryToken(page.RecoveryToken)
		if err != nil {
			return nil, err
		}
		if err = validateConsumerRecoveryTarget(inner, b, record); err != nil {
			return nil, err
		}
		data, _ := json.Marshal(activityConsumerRecoveryToken{Version: 1, Binding: m.consumerRecoveryBinding(b), Revision: record.Revision, Generation: record.Generation, Recovery: inner})
		out.RecoveryToken = base64.RawURLEncoding.EncodeToString(data)
		if len(out.RecoveryToken) > api.EnvironmentCacheActivityRecoveryTokenMaxBytes {
			return nil, invalid("response_too_large", "consumer recovery token exceeds its bound")
		}
	}
	out.Basis += " Consumer revision and generation are read-time guards, not a processing lease. Process the entire baseline before explicitly acknowledging; recovery saves no progress."
	return out, nil
}
