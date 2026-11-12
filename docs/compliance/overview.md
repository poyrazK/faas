# Compliance

How Gregale handles your data, and where to find the documents behind it.

## Data processing

- **[Data Processing Addendum](../DPA.md).** The DPA is part of the Gregale
  Terms of Service and applies to every customer that accepts them. It
  covers what Gregale processes on your behalf, how long it keeps it, the
  security measures in place, and breach notification.
- **[Sub-processors](subprocessors.md).** Every third party that processes
  customer data, what data it receives, and where. Gregale notifies customers
  at least 30 days before adding a sub-processor.

## Your data, under your control

- **Export and deletion.** You can export your account's data and request
  its deletion; a deletion has a grace period during which you can cancel
  it. See [Account and organizations](../account.md#export-and-deletion).
- **Secrets.** Secret values are sealed at rest and are never returned or
  logged. See [Delivered secrets](../secrets.md).
- **Isolation.** Every app instance runs in its own Firecracker microVM, and
  outbound connections to private and metadata addresses are blocked. See
  [Security](../security.md) and
  [Blocked outbound destinations](../egress-denylist.md).

## Reporting a vulnerability

Report security issues to `security@gregale.dev`. The
[responsible disclosure policy](responsible-disclosure.md) lists response
times, the PGP key, scope and safe-harbour terms.
