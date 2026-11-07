# Contained environment branch object storage through the bridge relay on metal, 2026-11-07

This report covers ADR-20261031193635 slice 6c on `gregale-internal-test-1` (GCP n2,
2 vCPU, KVM, cgroups v2), with the native harness (`/mnt/e2e-scratch/e2e-run.sh`):
`feat/environment-branches` at `94f0daebd9`, leakcheck before and after (clean both times).

## `TestEnvironmentBranchContainedObjectsMetal` — PASS (499 s)

apid ran with `FAAS_ENVIRONMENT_BRANCH_CONTAINMENT=1` and
`FAAS_ENVIRONMENT_BRANCH_CONTAINED_OBJECTS=1`. The test served the s3-gatewayd handler
in-process in front of an S3 stub, on the public TLS endpoint and on the loopback port
gatewayd-internal's relay forwards to (`FAAS_GATEWAYD_OBJECT_RELAY_UPSTREAM`).

1. A source-deployed Node app binds a read-write bucket in production.
2. `POST .../branches` with `egress: contained` returns 201, which the gate now
   allows despite the bucket. The data reconciler gives the branch an overlay bucket and
   a binding of its own.
3. Inside the branch guest, the binding's endpoint variable reads
   `http://objects.gregale:10085`.
4. The guest PUTs and then GETs an object through the relay, presigned with the
   branch's key. Both return 200, and the object lands in the branch's own physical
   bucket at the provider.
5. The same request presigned with the parent's key returns 403 `InvalidAccessKeyId`
   from the relay, and the parent key never reaches the gateway's relay port. The
   branch key does reach it.

The 6a contained profile passed again on this release
(`TestEnvironmentBranchContainedEgressMetal`, 476 s, run at `b7b28616de`).

## Earlier attempts

Both failures were in the test fixture, not the slice:

- `b7b28616de`: the fixture bound its bucket in a `production` value scope. An API-deployed
  app keeps its production values in `default`, so the branch had no bucket to overlay.
- `ed3f0bb507`: the guest's PUT crossed the relay and reached s3-gatewayd, which answered
  503 because the fixture had not seeded the bucket's usage accounting. This step already
  showed the guest's endpoint variable naming the relay, DNS, the netns port, the relay's
  caller and key checks, and the gateway's relay host and signature checks working on a
  real guest.
