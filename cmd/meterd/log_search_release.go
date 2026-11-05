package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// logSearchCapability is the catalog id whose flag meterd decides
// (pkg/productcap/catalog.json lists meterd as its consumer).
const logSearchCapability = "log-search"

// logSearchReleaseStore is the Store subset the decision reads.
type logSearchReleaseStore interface {
	AccountByID(ctx context.Context, id string) (state.Account, error)
	AppByID(ctx context.Context, id string) (state.App, error)
}

// logSearchRelease decides whether a firing alert carries the
// ADR-20261022172052 log context (ADR-20261024222102): only while the
// operator switch FAAS_LOG_SEARCH_ENABLED is on and the log-search capability
// flag (ADR-20261023115741) is released to the account, so an account that
// cannot open log search is not sent a link into it. The account and app are
// read once per fire, which each rule's cool-down bounds; a failed read
// decides off and the alert fires without the context.
type logSearchRelease struct {
	enabled bool
	decider *capflags.Decider
	store   logSearchReleaseStore
}

// newLogSearchRelease decides from flags, which meterd's runtime-config
// watcher keeps current.
func newLogSearchRelease(getenv func(string) string, flags *platformflags.Set, store logSearchReleaseStore) (*logSearchRelease, error) {
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("meterd: load capability catalog: %w", err)
	}
	return &logSearchRelease{enabled: getenv("FAAS_LOG_SEARCH_ENABLED") == "1", decider: decider, store: store}, nil
}

// Released reports whether log search is released to the account of appID.
func (l *logSearchRelease) Released(ctx context.Context, accountID, appID string) bool {
	if !l.enabled {
		return false
	}
	acct, err := l.store.AccountByID(ctx, accountID)
	if err != nil {
		return false
	}
	app, err := l.store.AppByID(ctx, appID)
	if err != nil {
		return false
	}
	return l.decider.Enabled(logSearchCapability, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: app.OrgID})
}
