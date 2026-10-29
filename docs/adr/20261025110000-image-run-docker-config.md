# Image runs read registry credentials from a Docker config

Date: 2026-10-25
Status: Accepted
Amends: ADR-1790 (image-run registry authentication)

## Context

ADR-1790 lets `gregale run --image` pull from a private registry with
`--registry-username` and a password from stdin, `GREGALE_REGISTRY_PASSWORD`
or a prompt. Someone who already ran `docker login` has those credentials in
Docker's config, often behind a credential helper (the macOS keychain, Docker
Desktop, `gh`), and had to find and paste them again.

## Decision

`gregale run --docker-config PATH` takes the image registry's credentials
from a Docker config, as Docker would use them.

- **Where.** `PATH` is a `config.json` or the directory holding it (`~/`
  expands), so `--docker-config ~/.docker` reads what `docker login` wrote.
  The flag is explicit: nothing is read without it.
- **Which entry.** The image's registry, as image references name it
  (`docker.io` for Docker Hub, matching its legacy
  `https://index.docker.io/v1/` key), selects the `auths` entry: its `auth`
  (`username:password` in base64) or its `username` and `password`. Without
  one, the registry's `credHelpers` helper, else the `credsStore` helper, runs
  as Docker runs it: `docker-credential-<name> get` with the server URL on
  stdin. A helper name must be one plain word, so a config cannot name a path.
  The helper call has a one-minute limit, because some helpers ask the user to
  unlock a keychain.
- **What is sent.** The result is the same username and password a
  `--registry-username` run sends (ADR-1790), stored sealed on the runner job
  for that registry only. Identity tokens (an `identitytoken` entry, or a helper
  answering `<token>`) and registry bearer tokens are refused, because a run
  hands the registry a username and password.
- **Exclusive.** `--docker-config` cannot be combined with
  `--registry-username`.
- **Errors** name the config file and registry and suggest `docker login`;
  they never include a credential.

## Consequences

- A developer who can `docker pull` a private image can run it with one flag.
- The CLI may start a credential helper the user configured; that is what
  Docker does with the same file.
- `gregale doctor --image` and deployment registry credentials are unchanged.
