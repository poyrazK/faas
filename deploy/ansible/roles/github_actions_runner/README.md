# `github_actions_runner`

Installs one pinned, repository-scoped GitHub Actions runner for the
provider-neutral `cd-compute` workflow. The runner is labeled `faas-fleet`
(or `faas-fleet-us` for the `production-us` fleet: set `faas_runner_labels`),
uses a dedicated non-root account, and is managed by a hardened systemd unit.

The role is intentionally conservative:

- it verifies Linux/x86_64/systemd and Debian-family prerequisites;
- it verifies the runner archive with a pinned SHA-256;
- it never replaces or deregisters an existing runner automatically;
- it reads the short-lived registration token only from the controller's
  `FAAS_RUNNER_REGISTRATION_TOKEN` environment variable;
- it fails if an existing installation is unmanaged, version-drifted, or
  partially configured.

## Bootstrap

The default target is the existing control-plane host. A dedicated management
runner can be targeted by supplying `faas_runner_hosts=fleet_runners` with an
inventory group of that name.

```sh
make ANSIBLE_INVENTORY=deploy/ansible/inventory/hosts.ini bootstrap-fleet-runner
```

The Make target obtains a short-lived repository registration token through an
authenticated `gh` installation, or uses an already-exported
`FAAS_RUNNER_REGISTRATION_TOKEN` when `gh` is not available. The token is
never placed in an Ansible variable file. The playbook installs the pinned
runner, registers it with the `faas-fleet` label, and starts
`faas-github-runner.service`.

After the service is online, `cd-compute` performs its own GitHub-hosted
preflight. That preflight checks both the required production environment
secrets and the online `faas-fleet` label before a deployment job can queue.

The role also creates `{{ faas_runner_enrollment_state | default('/var/lib/faas-runner/fleet-enrollment-used') }}` with mode `0700`.
The signed `FleetEnrollmentBundle` join path records one marker there after a
successful activation, so a retried workflow cannot reuse the same signed
authorization. Keep this directory on durable runner storage; do not place it
inside `_work` or a disposable container filesystem.
