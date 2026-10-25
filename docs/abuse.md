# Report abuse

Gregale runs applications for its customers. If an application served from
a `*.gregale.dev` address, or from a custom domain that points at Gregale, is
being used for phishing, malware, spam, attacks on other systems, or other
unlawful content, report it to us.

Security vulnerabilities in Gregale itself are different: report those to
`security@gregale.dev` as described in our [security policy](security).

## How to report

Email **`abuse@gregale.dev`** with:

- **Where:** the full URL or host name (for example
  `https://example-app.gregale.dev/login`). For network abuse, the source IP
  address, destination and port.
- **When:** the date and time you saw it, in UTC.
- **What:** a short description and your evidence, such as screenshots, the
  full headers of a phishing email, or log lines with timestamps for attack
  traffic.

Send a hash and the URL instead of attaching malware samples.

## What happens next

1. We find the application and the customer account behind your report.
2. For phishing, malware and attacks in progress, we suspend the account's
   workloads first and then investigate. A suspended application stops serving
   and cannot be redeployed while the suspension is in place.
3. For other reports, we contact the customer and give them a chance to
   respond before we act.
4. We reply when the report is closed. We do not share the customer's
   identity or data with reporters, except as the law requires.

## Abuse controls on every application

- Outbound mail ports (25, 465 and 587) are blocked for all applications.
  Mail sent through a third-party provider should also be reported to that
  provider.
- New outbound connections are rate-limited per instance.
- Applications cannot reach Gregale's internal networks or cloud metadata
  services.
