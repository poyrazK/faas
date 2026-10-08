# ADR-730: Recovery job discovery

Date: 2026-10-08
Status: Accepted

## Context

Operators must retain a recovery UUID to inspect or resume a job. Recovery controls need an application-scoped discovery surface for paused jobs and recent runs.

## Decision

Expose GET `/v1/apps/{slug}/event-recoveries`, requiring existing apps-read/admin scopes and MFA. The store verifies account ownership and a non-deleted app. Return at most 50 jobs, ordered by creation time descending then UUID descending, with an opaque keyset cursor bound to account, app and filters. Fetch one extra row to determine whether another page exists. Page size can change between requests.

Support exact admission state, routing/execution mode, subscription identifier, and exclusive RFC3339 creation bounds. An omitted captured mode means routing. Subscription filtering matches either the original selection filter or any frozen job item, including backfilled application execution recipients. No payloads or attempt history are returned.

List entries contain current admission counts, rate, pause time, and expiry. They omit execution observations; use recovery-status for those. The PostgreSQL query calculates admission counts only for the bounded page. Read application ownership and job metadata in a repeatable-read transaction without locks or mutations. Memory storage follows the same filtering and ordering behavior.

Pagination uses immutable creation keys; jobs created after the cursor do not shift later pages. State changes, deletion and retention remain live between pages: this is not a snapshot export. Cursors are navigation data, not credentials; every query independently enforces account and app ownership. Changing filters requires starting a new search.

Add an account/application/creation/UUID index through an append-only migration. No tables or columns are added, so the clone schema registry needs no entry.

## Usage

```sh
gregale events recovery-list my-app --state paused
gregale events recovery-list my-app --mode execution --subscription-id billing
gregale events recovery-list my-app --created-after 2026-10-01T00:00:00Z --limit 10 --json
# Repeat the same filters with --cursor <next_cursor> to continue.
gregale events recovery-status <job-id>
```

Go, Node and Python clients expose the listing operation. The existing 30-day terminal job retention and expiry processing are unchanged; a job awaiting expiry cleanup may still display its stored active state. Its expires_at value remains visible.
