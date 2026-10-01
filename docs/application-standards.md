# Application standards

Application standards describe organization-owned requirements for services.
Published versions are immutable, carry a canonical definition hash, and record
their publishing identity. Publishing creates a candidate; it does not activate
the version or change applications.

The implementation is in progress. Candidate version and resource management,
plus the durable automatic enrollment boundary and private persisted review
store, are implemented. Reviewed
assignment activation, control materialization, runtime enforcement and controlled rollout
must pass the acceptance checklist in [ADR-379](adr/379-inherited-application-standards.md)
before this feature is declared available.

## Enrollment boundary

Every organization-owned application insert records its original control values
and the explicit admission versions of matching active organization, project and
application assignments. This includes project plan/reconcile and PR preview
inserts. Publishing a candidate does not move an admission pointer or an existing
application's adoption. Restoring an application or changing its organization or
project rechecks inheritance and preserves its original values and local intent.

Enrollment keeps desired, persisted and observed revisions separate. Pending,
applying or blocked enrollment rejects deployment creation with HTTP 409 and
`application_standards_pending`. Persisting configuration will not count as
runtime observation. The public assignment activation and enrollment worker are
still being implemented; assignment fixtures currently exercise this boundary
in tests.

Legacy projects have account ownership rather than a dedicated organization
column. Assigning a project verifies the creator's organization membership and
every live member application's persisted organization. Shared project locks
on membership changes serialize that check against activation, including
cross-organization insert/restore races. An assignment retains its identity;
updates advance its revision, and direct deletion is reserved for organization
erasure.

## Reviewed changes under development

The private review store records organization, project or application assignment
changes, affected services, current controls, proposed controls, field provenance
and blockers. Reviews expire after 30 minutes. Their approval digest binds the
server-issued review identity, creator, assignment revision, admission version,
batch size, target membership, adoption pins, local intent, immutable resource
hashes, account entitlements and current artifact metadata. An unrelated candidate
publication or unused resource does not invalidate the reviewed version.

Freshness checks reread storage and reject changed inputs, expired reviews and
blockers. They also check inheritance for future services, including empty
projects. Existing services resolve their saved adoptions separately from the
versions offered to new services. An assignment disabled for new admissions can
remain adopted while its controlled removal proceeds.

An initial default preserves explicit existing settings. A logging requirement
that permits extra destinations preserves those extras separately, so replacing
the company's required destination does not turn the old destination into a
permanent extra. Quota review counts the entire proposed batch and existing
account-wide drains, including disabled destinations. Security changes to
existing artifacts require verification evidence; setting an enforcement flag
alone does not satisfy that gate.

Review records contain no destination URLs or credentials. Current destination
and credential inputs are represented by hashes. The schema also retains
immutable approved operation intent and target identities through service
deletion, with private fenced lease fields for subsequent worker integration.
Company attribution survives account erasure; owning organization erasure
removes its review and operation history.

The private approval path now locks and rereads the complete input set, checks
the exact digest and current approving authority, and commits the admission
pointer, frozen rollout targets and audit event in one transaction. A stale,
expired or blocked review writes none of them. Repeating the same approval
returns the original operation; another unfinished operation on the same
assignment blocks an overlapping change. Input locks use bounded retries so
concurrent legacy control writers cannot deadlock an approval's parent locks.
Restoring or reenrolling a service revokes its previous worker lease authority.

Approval leaves existing adoption pins and actual settings unchanged until the
service's rollout batch. New services capture the new admission version and
remain pending until materialization. Disabling admission likewise retains
existing pins until their approved removal. Saving an operation does not mark
any service persisted or observed.

These methods are internal storage interfaces. Public review/approval endpoints,
projection workers, consumer verification, exceptions, and rollback are still
being implemented. A successful read-only freshness check does not authorize an
unlocked mutation; writes must use the atomic approval path.

## Publish and inspect candidates

Create a definition file:

```json
{
  "require_signed": {"mode": "mandatory", "value": true},
  "security_policy": {"mode": "mandatory", "value": "enforce", "override": "narrow"},
  "egress_cidrs": {"mode": "restricted", "value": ["203.0.113.0/24"]},
  "egress_extra_ports": {"mode": "restricted", "value": [5432]}
}
```

The example network is a documentation range; select actual approved
destinations before applying a standard. Plan and platform network restrictions
still apply.

```bash
gregale orgs standards publish --org acme --standard production-baseline \
  --file standard.json --expected-version 0 --description "Production baseline"
gregale orgs standards list --org acme
gregale orgs standards show --org acme --standard production-baseline --version 1
```

For an update, supply the latest version as `--expected-version`. A concurrent
publication returns `application_standard_version_stale`; refresh and review
before retrying. Read an older version with `--version`, or omit it for the latest
candidate. List responses include `next_page_after`; pass it as `--after` to
retrieve the next page. Output is structured JSON.

Owners and admins publish versions. Active organization members can inspect
candidate definitions. Requests require the existing authentication, MFA and API
key scope checks in addition to the organization action.

## Organization-owned resources

Create logging destinations and approved publishers once per organization.
Use the returned resource UUIDs in `log_destinations` and `trusted_publishers`.
Publication rejects missing resources and resources owned by another organization.

For a logging destination, save a private JSON file:

```json
{
  "name": "Central production logs",
  "kind": "http_json",
  "target_url": "https://logs.example.com/ingest",
  "auth_header": "Authorization: Bearer REPLACE_WITH_CREDENTIAL"
}
```

The endpoint must use HTTPS and cannot contain userinfo, query strings or
fragments. Credentials belong in the optional header and are sealed server-side.
Read/list responses, standard definitions and audit events omit credential material.

```bash
gregale orgs standards destinations create --org acme --file destination.json
gregale orgs standards destinations list --org acme
gregale orgs standards destinations show --org acme --id DESTINATION_UUID
```

A publisher file contains `name` and `public_key_der`, the base64-encoded ECDSA
P-256 SubjectPublicKeyInfo DER supported by Gregale's image verifier. Private
keys, malformed keys and unsupported curves are rejected. Publisher responses
include the public key and its SHA-256 fingerprint.

```bash
gregale orgs standards publishers create --org acme --file publisher.json
gregale orgs standards publishers list --org acme
gregale orgs standards publishers show --org acme --id PUBLISHER_UUID
```

Resources are immutable, including destination credentials. Rotation creates a
new resource and a new standard version referencing it; existing services change
through the controlled adoption process. Creating a resource alone changes no
service. Emergency revocation and adoption still require the remaining acceptance
work; these endpoints are candidate management during implementation.

## Requirement semantics

| Mode | Meaning |
|---|---|
| `default` | Supply an inherited value when local intent does not replace it. |
| `mandatory` | Require the value; changes obey the declared override rule. |
| `restricted` | Bound the permitted value and allow narrowing only. |

An omitted override is `none` for a mandatory requirement and `narrow` for a
restricted requirement. `extend` is valid only for mandatory log destinations:
all required destinations stay present while additional destinations are
permitted. `narrow` permits stronger signature/posture requirements, a smaller
publisher or extra-port set, and CIDRs contained by the approved ranges.

Supported fields are `log_destinations`, `require_signed`, `security_policy`,
`trusted_publishers`, `egress_cidrs`, and `egress_extra_ports`. Destination and
publisher sets contain organization-owned resource UUID references. Credential
values do not belong in the definition. Unsupported fields, duplicate JSON keys,
unknown rule properties, and incompatible override modes are rejected.

Clearing `egress_cidrs` is unrestricted access in Gregale's network contract; it
does not narrow a nonempty approved range. Disjoint inherited CIDR bounds are
reported as a conflict instead of becoming an unrestricted empty set. Extra
ports retain the existing platform contract, including the base ports and
forbidden-port restrictions.

More-specific scopes cannot weaken mandatory ancestor requirements. Effective
settings retain every contributing standard, version, scope and applicable
exception identifier. Approved exceptions affect only the named field and
immutable version; at their exact expiry boundary the ordinary requirement
applies again. Independent requirements remain enforceable.
