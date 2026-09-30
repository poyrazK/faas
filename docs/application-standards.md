# Application standards

Application standards describe organization-owned requirements for services.
Published versions are immutable, carry a canonical definition hash, and record
their publishing identity. Publishing creates a candidate; it does not activate
the version or change applications.

The implementation is in progress. Candidate version management is implemented;
assignment, automatic enrollment, runtime enforcement and controlled rollout
must pass the acceptance checklist in [ADR-379](adr/379-inherited-application-standards.md)
before this feature is declared available.

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
