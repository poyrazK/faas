# ADR-20261021113000: Image workloads in atomic releases

## Status

Implemented. Amends ADR-20261019120004 (safe project source releases).

## Context

A project workload can run a prebuilt image declared in its source, for
example a compose service with `image: ghcr.io/acme/api:2`. Ordinary project
apply rebuilds such a workload through the same queue as source workloads; the
builder converts the image, and the deployment passes the same readiness
stage. The first atomic release version refused any image declaration, so a
project with one image service could not release atomically.

## Decision

An existing image workload may take part in an atomic release:

- Its image reference is the release's source. Changing the reference (a new
  tag or digest) is allowed; the port, command, healthcheck, process and every
  other setting must stay unchanged, exactly as for source workloads.
- A workload cannot switch between source and image in an atomic release.
- The candidate deployment runs the scanned reference at zero traffic and joins
  the release graph only after readiness, like source candidates. The serving
  app's record is not reconciled: atomic releases do not advance source
  checkpoints either, and the deployment records the image it runs.

Release commands, workers and every other atomic release rule apply unchanged.

## Consequences

Mixed projects of source and image services can release atomically, and a
failed image candidate leaves the previous graph serving. Because the app record
keeps its earlier reference, an ordinary scan after an atomic release reports
the image as changed, as it reports source changes today.

## Validation

`TestProjectSafeSourceImageWorkloads` covers a new reference, a changed port and
both switches between source and image.
