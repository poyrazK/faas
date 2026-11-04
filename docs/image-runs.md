# Image runs

Run an OCI image once in a fresh microVM. This preview moved out of the
[jobs page](jobs.md) so that page can be published (#5709); it is not
published while the platform operator enables it per installation.

> **Preview.** Image runs are enabled by the platform operator. Until
> `FAAS_IMAGE_RUNS=1` is set on the control plane, `POST /v1/image-runs`
> returns 404. Interactive runs (`-it`) also need `FAAS_INTERACTIVE_IMAGE_RUNS=1`;
> without it they return 501.

`gregale run --image IMAGE [ARG...]` runs one command from an OCI image in a
fresh microVM, the way `docker run` does (ADR-1161):

```sh
gregale run --image alpine echo hello
gregale run --image python:3.12-slim -e MODE=batch python -c 'print(42)'
gregale run --image my-registry.example/tool:1.4 --entrypoint /bin/sh -- -c 'tool --version'
```

Arguments after the flags replace the image `CMD`; the image `ENTRYPOINT`
stays unless `--entrypoint` overrides it, which also drops the image `CMD`.
The command waits for the run, prints its output, and exits with the
command's exit code (124 after the deadline, 130 when cancelled). Ctrl-C
cancels the run. `--detach` prints the run id instead of waiting.

Each image run is a one-task Job run with no retries. Gregale keeps a
platform-managed runner job per image (named `ir-<hash>`), so the first run
pulls the image and later runs start from the cached copy. `--pull always`
pulls a moved tag again. Runner jobs do not appear in `gregale job list` and
do not count against your Job limit; inspect a run with
`gregale job tasks ir-<hash> RUN_ID` and `gregale job logs ir-<hash> RUN_ID`,
or delete a cached image with `gregale job delete ir-<hash>`.

Image runs use your plan's Job limits for memory, deadline (by default the
plan's maximum) and concurrent tasks. Plans cache a limited number of images
(`api.ImageRunCachedImages`: Hobby 3, Pro 10, Scale 25); starting a run for a
new image evicts the least recently used cached image that has no queued or
running work. Free plans do not include Jobs or image runs. Output streams
while the command runs (ADR-1163).

The command runs the way `docker run` runs it (ADR-1790): with the image's
`ENV` (your `-e` and `--env-file` values win), as the image's `USER` (root
when the image sets none), in the image's `WORKDIR`, and a bare command name
is looked up on the image `PATH`. Docker's flags override them:

```sh
gregale run -u nobody -w /data alpine sh -c 'id; pwd'
gregale run --env-file .env -e DEBUG -e MODE=batch python:3.12-slim python job.py
```

- `-u/--user name|uid[:group|gid]` resolves in the image's own passwd and
  group files.
- `-w/--workdir DIR` takes an absolute path and creates it when missing.
- `--env-file FILE` reads docker's env-file format: `KEY=VALUE` or `KEY` per
  line, `#` comments, values taken literally. `-e KEY` without a value (or a
  bare `KEY` line) passes your local value and is skipped when it is unset.
  Files apply in order, then `-e` flags.
- The image can be the first argument instead of `--image`.

For a private registry, pass `--registry-username` and the password or token
on stdin (`--registry-password-stdin`), in `GREGALE_REGISTRY_PASSWORD`, or at
the prompt:

```sh
printf '%s' "$GHCR_TOKEN" | gregale run --registry-username ci --registry-password-stdin ghcr.io/acme/tool:1.4 --version
```

If you already ran `docker login`, `--docker-config` uses those credentials
instead
([ADR-20261025110000](adr/20261025110000-image-run-docker-config.md)):

```sh
gregale run --docker-config ~/.docker ghcr.io/acme/tool:1.4 --version
```

It reads the image registry's entry from that `config.json` (or the
directory holding it), or asks the credential helper Docker is configured
with, such as the macOS keychain or Docker Desktop. Identity tokens are
not supported; use `--registry-username` with an access token for those
registries.

The credential is stored sealed on the image's runner job and is used for
that image's registry only. Every run that passes it refreshes it, so it
survives the runner job being evicted from the image cache. Without it, a
runner job's credential can still be managed with
`gregale job registry set ir-<hash> ...`.

## Interactive image runs

`gregale run -it --image IMAGE [ARG...]` attaches your terminal to the
command, the way `docker run -it` does (ADR-1330):

```sh
gregale run -it --image alpine sh
gregale run -i --image python:3.12-slim python -c 'print(input())' < answer.txt
```

`-i` connects stdin; `-t` also gives the command a terminal and needs `-i`.
Without a terminal on your side, `-t` is dropped with a warning. The run's VM
waits up to 60 seconds for the session before it starts the command, and the
run's timeout (at most an hour) bounds the session. The CLI exits with the
command's exit code. Ctrl-C before the session starts cancels the run; after
that it reaches the remote terminal. Closing the session ends the command
(the task finishes `cancelled`). Interactive runs keep no output: `jobs logs`
is empty for them, and a failed interactive run cannot be replayed or
retried; start a new run instead.
`--detach` and `--json` do not apply.
