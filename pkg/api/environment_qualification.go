package api

// Guest-only route. Graph/binding selectors carry no authentication authority;
// the listener resolves the actual original VM frame from its network slot.
const EnvironmentQualificationServicePrefix = "/v1/internal/environment-qualification/"

// EnvironmentQualificationQueuePrefix is the guest-only lease route of the
// node-local qualification proxy (ADR-20261022155812). A candidate's pull
// consumer appends the ordinary /v1/apps/{slug}/queues/{queue}/leases path;
// the listener resolves the candidate from its network slot and serves only
// that attempt's reviewed smoke message.
const EnvironmentQualificationQueuePrefix = "/v1/internal/environment-qualification-queues"

// QueueLeaseURLEnv is the platform-owned lease base Gregale stamps into a
// qualification candidate with a pull binding. When it is set, a pull
// consumer leases there without a credential, in place of the public API.
const QueueLeaseURLEnv = "GREGALE_QUEUE_LEASE_URL"
