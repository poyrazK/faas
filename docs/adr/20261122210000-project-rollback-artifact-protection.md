# Protect project deploy rollback artifacts

Status: Accepted

Use `gregale project rollback <slug> --protect-deploy <uuid> --ttl 168h` to protect the artifacts for a recorded production deploy. The CLI defaults to seven days and accepts durations from one second through seven days. Repeating protection for the same artifact set can extend its expiry.

Gregale verifies the recorded members' root filesystems, sidecar layers, and signatures. If any member or artifact is unavailable, the request fails before saving a hold. PostgreSQL checks the verified inventory again and locks each storage key in the same order as existing layer retention before it saves a hold. The database guards older workers' deletion claims during a rolling upgrade.

`--targets` shows the expiry and unique layer bytes for a hold that matches the deploy's current recorded artifact set. The garbage collector can claim those layers after expiry when its other retention checks allow deletion. A restore runs fresh rollback preflight and uses the usual preparation and cutover workflow.
