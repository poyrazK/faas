package sched

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Capability flags (ADR-20261023115741): schedd holds the background work of
// a capability whose platform flag is off for the owning account. Held work
// is deferred, never failed or dropped, and resumes when the flag is on.

// capabilityDeferral is how long held work waits before schedd decides again.
const capabilityDeferral = time.Minute

// capabilitySubjectTTL bounds how long a resolved flag subject is reused.
const capabilitySubjectTTL = 30 * time.Second

type capabilitySubjects struct {
	mu      sync.Mutex
	entries map[string]capabilitySubjectEntry
}

type capabilitySubjectEntry struct {
	subject platformflags.Subject
	expires time.Time
}

func (c *capabilitySubjects) get(key string, now time.Time) (platformflags.Subject, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.expires) {
		return platformflags.Subject{}, false
	}
	return entry.subject, true
}

func (c *capabilitySubjects) put(key string, subject platformflags.Subject, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]capabilitySubjectEntry{}
	}
	c.entries[key] = capabilitySubjectEntry{subject: subject, expires: now.Add(capabilitySubjectTTL)}
}

// WithCapabilities installs the capability flag decider. Nil (the default and
// every unit test) releases every capability.
func (e *Engine) WithCapabilities(d *capflags.Decider) *Engine {
	e.capabilities = d
	return e
}

// appCapabilityAllowed decides capability for the account that owns app: the
// resource account's id and plan, and the app's organization. A failed lookup
// allows the work, so a transient store error never parks a whole fleet.
func (e *Engine) appCapabilityAllowed(ctx context.Context, app state.App, capability string) bool {
	if e == nil || e.capabilities == nil || capability == "" {
		return true
	}
	now := time.Now()
	subject, ok := e.capabilitySubjects.get("app:"+app.ID, now)
	if !ok {
		acct, err := state.AppResourceAccount(ctx, e.store, app)
		if err != nil {
			return true
		}
		subject = platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: app.OrgID}
		e.capabilitySubjects.put("app:"+app.ID, subject, now)
	}
	return e.capabilities.Enabled(capability, subject)
}

// EgressCircuitAllowed reports whether egress circuit breaking applies to the
// account that owns appID; the egress circuit loop clears the circuits of an
// app it refuses.
func (e *Engine) EgressCircuitAllowed(ctx context.Context, appID string) bool {
	return e.appIDCapabilityAllowed(ctx, appID, "egress-circuit-breaking")
}

// appIDCapabilityAllowed is appCapabilityAllowed for an app known by id.
func (e *Engine) appIDCapabilityAllowed(ctx context.Context, appID, capability string) bool {
	if e == nil || e.capabilities == nil || capability == "" || appID == "" {
		return true
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return true
	}
	return e.appCapabilityAllowed(ctx, app, capability)
}

// accountCapabilityAllowed decides capability for an account-scoped run.
func (e *Engine) accountCapabilityAllowed(acct state.Account, capability string) bool {
	if e == nil || e.capabilities == nil || capability == "" {
		return true
	}
	return e.capabilities.Enabled(capability, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
}

// invocationCapability is the capability whose flag gates an invocation's
// delivery, by the invocation's source.
func invocationCapability(source state.InvocationSource) string {
	switch source {
	case state.InvocationCron:
		return "crons"
	case state.InvocationDelayedTask:
		return "delayed-execution"
	case state.InvocationAsyncInvoke, state.InvocationReplay:
		return "async-invocations"
	case state.InvocationInboundWebhook:
		return "durable-inbound-webhooks"
	case state.InvocationQueue:
		return "queues"
	case state.InvocationExclusiveOperation:
		return "managed-exclusive-operations"
	}
	return ""
}

// triggerCapability is the capability whose flag gates a trigger's polling.
func triggerCapability(kind string) string {
	switch kind {
	case string(api.TriggerKindCron):
		return "crons"
	case string(api.TriggerKindQueue):
		return "queues"
	case string(api.TriggerKindRealtime):
		return "realtime"
	case "":
		return ""
	}
	return "event-triggers"
}
