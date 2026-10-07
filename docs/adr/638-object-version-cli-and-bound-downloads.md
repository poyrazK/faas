# ADR-638: Object version listing and bound historical downloads

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Expose retained versions through a scoped control API and the
  Gregale CLI. Translate native IDs through the same durable references used by
  the S3 gateway. Persist an optional immutable public version ID in GET/HEAD URL
  authority and require that exact selector at the gateway. Native providers
  receive only the owned reference's resolved native ID.
- **Why:** Customers could read historical versions with S3 SDKs, but the Gregale
  CLI could neither discover those versions nor download an older generation.
  A signed current-object URL must not silently become authority for a different
  generation, and a historical download must not substitute the current object.
- **Consequences:** Listings support paired pagination, prefix and delimiter,
  including delete markers and the mutable S3 `null` version. Fixed historical
  download URLs accept only canonical non-null public UUIDs. Listing and URL
  issuance retain bucket grants, accounting admission and revocation. Database
  constraints validate the new descriptor; rollback refuses to discard stored
  version authority. The CLI publishes downloaded files only after an exact
  acknowledged public version and a complete transfer. Upload results include
  the public version ID when the provider supplies one.
- **Rejected alternatives:** Exposing provider generations to clients; fetching
  the current object after a selected version disappears; trusting a URL query
  selector without binding it to persisted authority; treating mutable `null`
  as an immutable historical download.

This extends [ADR-542](542-customer-s3-version-identities-and-reads.md) and
[ADR-557](557-branded-object-url-capabilities.md) without changing provider
versioning configuration, write admission or retention policy.
