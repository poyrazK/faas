# ADR 397: Publish bounded edge certificate evidence

Status: proposed

The public TCP edge needs to distinguish enabled termination intent from usable local certificate material. During listener reconciliation, the supervisor validates the current certificate and publishes hostname, listener-intent version, edge identity, observation time, and expiry to the observation store. Ownership and termination policy continue to be checked separately before target selection.

Unchanged evidence is written at most once per 15-second heartbeat; changed evidence is written immediately. Publication has a two-second context deadline, retries failed writes, tolerates intent conflicts, and prunes expired evidence. Store failure does not prevent socket reconciliation. Observations expire after 60 seconds and cannot establish fleet coverage or guest readiness.

Aggregate Prometheus gauges report certificate-ready and certificate-not-ready listener counts and earliest certificate expiry without hostname labels. Supervisor shutdown clears the gauges. A valid bounded edge identity is mandatory when publication is configured.

Validation: race tests cover publication heartbeat, change, cancellation, retry, and certificate rejection. A composed real-socket TLS test covers missing bundles, provisioning, atomic rotation, published evidence, metrics, admission, and disabling an established listener. Scheduler and guest execution are substituted; native Linux/amd64 KVM acceptance remains required.
