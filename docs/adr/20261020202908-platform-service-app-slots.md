# Unmodified platform service apps do not use a deployed-app slot

Date: 2026-10-20
Status: Proposed (needs the owner's sign-off and a matching update to the financial model before implementation)
Related: ADR-1190 (application Auth), ADR-20261018170742 (Data API access modes), spec §4.7 (billing), `pkg/api/limits.go` (`DeployedApps`), `docs/plans.md`

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

An Auth app (`gregale auth create`) and a Data API app are ordinary deployed
apps for plan quotas. The Free plan allows one deployed app, so a Free
account can run its own app or Auth (or a Data API), never both; the Data API
ADR already records that "a Free account cannot run the starter". Every
competing auth product is free to start: Clerk to 50,000 retained users,
Auth0 to 25,000, Supabase and Firebase to 50,000, WorkOS to 1 million. The
evaluation path for Gregale Auth therefore fails before a developer sees the
features it is strong on (organizations, SSO and SCIM without per-connection
fees, data in their own Postgres).

Gregale sells compute only: software features are free, and features that
hold a real resource get plan limits sized to that resource, never a new
meter. A deployed-app slot exists to bound what an app holds while parked:
its app layer and its snapshot on disk, and its catalog rows. An Auth or Data
API app still runs on the account's compute, which stays billed (plan RAM +
8 MB per running second, spec §4.7), so the slot is the only thing to
decide.

## Decision

1. **Platform service apps.** An app is a platform service app of kind `auth`
   or `data_api` while its current deployment was built from an unmodified
   platform template: the source tarball's SHA-256 is one apid publishes for
   that template in this or the previous release (N-1, so a CLI one release
   behind still qualifies). The CLI already packs a template from fixed
   files; packing becomes byte-reproducible (sorted entries, fixed mode,
   owner and mtime, fixed gzip header), and the release publishes the
   digests next to the template catalog. Configuration lives in app secrets,
   so every account's Auth app has the same source.
2. **Slots.** On every plan, one platform service app of each kind per
   account does not count toward `DeployedApps`. Further ones count as
   ordinary apps. Nothing else changes: compute, concurrency, RAM per app,
   idle timeout and Free's 20 GB-h allowance apply as to any app.
3. **Losing the exemption.** A deployment of any other source to that app
   clears the mark. If the account is then over `DeployedApps`, the deploy is
   refused with the existing RFC 7807 limit error (limit, observed count,
   docs link), so the exemption cannot be turned into a free slot for
   arbitrary code.
4. **Where.** apid sets `apps.platform_service` (`auth`, `data_api` or null,
   with a CHECK) at deploy time from the uploaded source digest;
   `CountDeployedApps` excludes the first app of each kind; the per-plan
   number of exempt apps is a `PlatformServiceApps` entry in `limits.go`
   (1 on every plan), rendered into `docs/plans.md`. The migration is an
   expand-only nullable column with `SET LOCAL lock_timeout = '5s'`.

## Cost

The exemption adds, per Free account that uses it, one parked app: its layer
and one snapshot. The fleet target is 130 MB per snapshot
(`snapshot_fleet_avg_mb` alerts at 160); production measured about 615 MB per
snapshot on 2026-10-07, before ADR-633's block sharing, mostly a full copy of
the app layer. An idle Auth instance has 60–100 MiB resident (measured
natively on 2026-10-20), so its memory image is small; its layer (Node,
GoTrue and the front) is the larger part and is identical across accounts,
which is the case block sharing is for. The staging canary records the exact
snapshot and layer bytes. Compute is unchanged: an Auth app awake all month
at 256 MB is about 187 GB-h, far above Free's 20 GB-h, so on Free it stays an
evaluation tool and the allowance stops it as for any app.

The financial model's Free disk assumption (one parked app per Free account)
must be updated to two for accounts that use Auth or a Data API before this
ships; this record does not change the model.

## Alternatives rejected

- **Raise Free to two deployed apps.** Gives every Free account a second slot
  for arbitrary code, doubling Free's parked-app disk whether or not the
  account uses Auth, and changes a headline plan number.
- **Exempt any app with an `auth` credential binding.** The binding is
  attached by the owner and survives any later deploy, so it would be a free
  slot for arbitrary code.
- **Run Auth and Data API as shared multi-tenant platform services.** Removes
  the slot entirely but abandons the per-account isolation, scale-to-zero
  and data-in-your-Postgres model that ADR-1190 is built on.
- **Price Auth separately.** Contradicts the compute-only pricing rule.
