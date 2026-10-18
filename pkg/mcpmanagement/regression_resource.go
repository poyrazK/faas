package mcpmanagement

import (
	"context"
	"encoding/json"
	"time"
)

const regressionResourceTemplate = "gregale://apps/{app}/regressions"

type regressionResourceView struct {
	regressionsView
	SnapshotObservedAt string `json:"snapshot_observed_at"`
	UpdateSemantics    string `json:"update_semantics"`
}

func (m *management) regressionResource(ctx context.Context, args appArgs, expectedAppID string) (*guardedRead, error) {
	if args.Project != "" || args.Environment != "" {
		return nil, invalid("invalid_resource", "regression resources are app-wide, not environment-scoped")
	}
	read, err := m.readRegressions(ctx, regressionsArgs{App: args.App, Since: "1h"}, expectedAppID)
	if err != nil {
		return nil, err
	}
	read.value = regressionResourceView{regressionsView: read.value.(regressionsView), SnapshotObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		UpdateSemantics: "Sampled current visibility, not an event log or atomic snapshot. URI-only invalidations require a fresh read; reread after reconnecting and explicitly resubscribe. Intermediate changes may be missed. Disappearance can mean dismissal, resolution, lookback expiry or projection ordering/truncation; it does not prove recovery. Changes outside the projected prefix may not notify. No automatic triage or other writes."}
	return read, nil
}

// Match the tool wrapper: authorize first, then check the guarded identities
// immediately before serializing. Never marshal the private guard closure.
func checkedResourceValue(ctx context.Context, value any) (any, error) {
	if read, ok := value.(*guardedRead); ok {
		if err := read.check(ctx); err != nil {
			return nil, err
		}
		return read.value, nil
	}
	return value, nil
}

func (m *management) regressionSubscriptionSnapshot(ctx context.Context, args appArgs, pin *resourceSubscription) (*resourceSubscription, [32]byte, error) {
	var zero [32]byte
	expected := ""
	if pin != nil {
		expected = pin.appID
	}
	read, err := m.regressionResource(ctx, args, expected)
	if err != nil {
		return nil, zero, err
	}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return nil, zero, err
	}
	value, err := checkedResourceValue(ctx, read)
	if err != nil {
		return nil, zero, err
	}
	safe, err := m.safeOutput(value)
	if err != nil {
		return nil, zero, err
	}
	digest, err := resourceSemanticDigest(safe.(json.RawMessage), time.Now())
	return &resourceSubscription{args: args, view: "regressions", appID: value.(regressionResourceView).AppID}, digest, err
}
