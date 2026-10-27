# ADR-1790 · Image runs: Docker process semantics, `-u/-w`, env files and private registries

- **Status:** accepted
- **Date:** 2026-10-18
- **Amended by:** ADR-20261025110000 (credentials from a Docker config)
- **Amends:** ADR-1161 (image runs; closes its private-registry follow-up),
  ADR-1239 (applies its image identity to image-run jobs)
- **Decision:** An image run executes with the image's `ENV`, `USER` and
  `WORKDIR` and resolves a bare command on the image `PATH`, as `docker run`
  does. `gregale run` gains docker's `-u/--user`, `-w/--workdir`,
  `--env-file`, `-e KEY` pass-through and a positional image, and
  `--registry-username` for private registries. The credential is stored
  sealed on the run's runner job by the same code as
  `PUT /v1/jobs/{name}/registry-credentials`, so no account-wide credential is
  introduced.
- **Why:** In job mode guest-init ran the command with only the kernel's init
  environment (no `PATH`, no image `ENV`), as root, in `/`. So
  `gregale run --image alpine echo hi` could not find `echo`, and an image
  with `USER` or `WORKDIR` ran with neither. ADR-1239 already said image-run
  jobs follow the image's identity. Private images needed the user to find
  the hidden `ir-<hash>` runner job and set its credential, which LRU
  eviction then deleted.

## Process semantics

schedd stages `image_process: true` in the task's `job.json` for runner jobs
of kind `image_run`. With it, guest-init reads the image process config from
`/etc/faas/app.json` (imaged writes it from the OCI config when it
materializes the job image) and runs the command:

- **Environment.** Layered as guest environment < image `ENV` < run env
  (`-e`, `--env-file`) < secrets < the platform job baseline. An image without
  `PATH` gets Docker's default. `HOME` comes from the image passwd entry of the
  process user when neither the image nor the run sets it, or `/` without an
  entry.
- **User.** `-u` if given, else the image `USER`, else root (ADR-1239). Names
  and groups resolve in the image's own `/etc/passwd` and `/etc/group` with
  the ADR-1239 rules. For a TTY session (ADR-1330) the terminal belongs to that
  user.
- **Working directory.** `-w` if given, else the image `WORKDIR`, else `/`. A
  missing directory is created and owned by the process user.
- **Command lookup.** A bare command name resolves on the image `PATH`, as app
  tasks do.

Ordinary Jobs are unchanged: they keep running as root with the guest
environment. Bringing them to the same semantics changes existing customer
jobs and is a separate decision.

`-u` and `-w` are snapshotted on the run (`job_runs.process_user`,
`process_workdir`, copied by `replay-failed`) and reach the guest as
`job.json` `user` and `workdir`. apid validates them (`name|uid[:group|gid]`
by `ociidentity.ValidateSpec`, at most 256 bytes; an absolute path of at most
4096 bytes), and vmmd and fcvm validate them again. They are valid only with
`image_process`.

## Private registries

`POST /v1/image-runs` accepts `registry_auth: {username, password}`. apid
binds it to the image's registry API host (the key imaged uses), applies the
job credential plan gate and quota, seals the password with the
`registry_creds` namespace, and upserts it on the runner job before imaged is
told to pull. Every request that carries it refreshes it, so a runner job
evicted and recreated by the LRU cache gets it back on the next run that
supplies it. A runner whose earlier pull failed is re-queued by the same
request. The password never appears in logs, audit payloads, responses or
`Idempotency-Key` replay (only responses are stored).

The CLI takes `--registry-username` and reads the password from
`--registry-password-stdin`, `$GREGALE_REGISTRY_PASSWORD`, or a terminal
prompt. There is no password flag, so it never lands in shell history. It
sends the credential only on the first request, not on the polls while the
image is pulled. `--registry-password-stdin` cannot be combined with `-i`,
because stdin is the session. The CLI does not read `~/.docker/config.json`:
credential helpers are common, and sending local Docker credentials to
Gregale implicitly would surprise users. Reading it can be added later behind
an explicit flag.

## CLI

- `gregale run [flags] IMAGE [ARG...]` is equivalent to `--image IMAGE`. Image
  mode is selected by `--image`, or by a positional argument when none of the
  disposable-run flags (ADR-171: `--runtime`, `--source`, `--file`, `--dir`,
  ...) is present; disposable runs take no positional arguments, so this is
  unambiguous.
- `--env-file` follows docker's format: `KEY=VALUE` or `KEY` per line, leading
  whitespace ignored, `#` comments and blank lines skipped, values literal.
  Files apply in order, then `-e` flags; later values win. `KEY` alone (in a
  file or `-e`) passes the local value and is skipped when unset.

## Rollout and compatibility

The new `job.json` fields fail closed on an older guest-init, which rejects
unknown fields: every image run fails with an infrastructure error until the
platform base image carries the new guest-init. vmmd echoes
`image_process` in `JobColdBootResponse`; when an older vmmd does not, schedd
fails the task (`job_vmm_image_process_unsupported`) instead of running it with
the old semantics. Roll out the base image, vmmd and schedd before apid starts
accepting `user`, `workdir` and `registry_auth`. Image runs stay behind
`FAAS_IMAGE_RUNS`, which is off by default.

## Consequences

- Image runs behave like `docker run` for identity, environment and working
  directory, including official images that set `USER`.
- User and workdir are per run, like args, entrypoint and env; the cached
  runner job and its artifact are shared across runs of the image.
- Native acceptance (`TestImageRunProcessMetal`) covers the image defaults
  (USER, a missing WORKDIR, ENV, a bare command on the image PATH, HOME from
  passwd) and the `-u/-w` overrides. It has not run, because the native
  acceptance VM is gone. The fake registry has no authentication, so an
  authenticated pull is covered by unit tests only.

## Rejected alternatives

- **Record USER/WORKDIR/ENV in the jobs table and pass them through schedd.**
  imaged already writes them into the artifact's `app.json`, which app tasks
  read the same way; a second copy could drift from the artifact.
- **Account-wide registry credentials.** ADR-062 rejected them; per-job
  credentials refreshed by the run keep the ownership model.
- **A `--registry-password` flag.** It would land in shell history and process
  listings.
