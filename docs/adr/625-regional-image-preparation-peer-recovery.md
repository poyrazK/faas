# ADR-625: Regional image preparation peer recovery

Date: 2026-10-05
Status: Implemented; portable validation recorded separately. Native runtime acceptance remains pending.

## Context

Durable image preparation checkpoints were introduced upstream while regional
builder exports were implemented on this branch. A checkpoint that retains the
producer's local OCI path cannot be consumed by another regional worker after
the producer disappears. Regional conversion must retain checkpoint recovery,
verified export references, and selected-region placement together.

## Decision

Use the deployment activation lock and preparation claim token for regional
exports as well as local inputs. Pin the immutable export key, byte count and
SHA-256 in the existing private input fields. Every conversion fetch verifies
that exact descriptor and the bytes obtained from the regional storage route.
A transient fetch failure remains recoverable. Replacing the descriptor fails
closed. Never open the historical producer path for a regional export.

Shared regional exports have a region-owned preparation rather than a worker
owner. Before claiming or resuming, require an active registered worker in the
app's selected compute region. Filter recovery sweeps to eligible regional
workers before bounding their results. The deployment lock serializes peers;
replaced tokens cannot publish metadata. Resume from layer publication, scan
completion and scheduler handoff checkpoints without repeating completed work.
Other inputs retain their existing worker ownership and local export leases.

A preparation that predates checkpoints returns a distinct legacy-in-flight
outcome. Under the same deployment lock, resume its existing regional stage
without fabricating a checkpoint or treating its published ext4 as OCI input.
A completed checkpoint remains a skipped outcome and does not enter that path.
Ineligible workers return the durable notification ownership outcome so they
cannot acknowledge work assigned to another eligible consumer.

This decision does not activate regional runtime quotas, publish global ingress,
or replace dedicated native KVM lifecycle acceptance.

## Validation

Exercise connected producer-to-peer image handoff for apps and functions after
the producer export is removed. Inject a failure after layer publication and
recover with another eligible peer; assert one conversion and eventual scheduler
handoff. Check descriptor and recovery ownership in memory and PostgreSQL, and
run the image, builder, state and lifecycle race suites. Verify that legacy
materialization and checkpoint preparation exclude each other under the same
activation lock, and distinguish legacy in-flight work from completed checkpoints.
