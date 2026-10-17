# ADR-1352 · Contained rehearsal commands

- **Status:** accepted; command enrollment disabled pending native qualification
- **Date:** 2026-10-17
- **Decision:** Execute opted-in rehearsal recipe commands in fresh deployment-attached app-task microVMs. Persist a versioned command envelope in the existing immutable task argv and a matching guest receipt in the task output. Require confirmed VM cleanup and settled recorder obligations before recording command retirement.
- **Why:** Host recipe commands can perform real external actions and have no serving-VM recorder. A passing host command cannot establish contained execution.
- **Consequences:** Reuse scheduler-owned task admission, leases, at-most-once dispatch, cancellation, scoped deployment environment, and bounded output. No host process executes an opted-in command and no host directory is mounted into a guest. Each command starts from the pinned deployment; filesystem changes do not carry between commands.
- **Rejected alternatives:** Host shell wrappers, container execution, mounting checkout directories, treating exit zero as a cleanup receipt, or adopting a recorder after owner loss.

The reserved `__gregale_rehearsal_command_v1__` argv envelope is interpreted by
guest-init, using batch wire version 3 on the deployment-attached task channel.
Versions 1 (ordinary batch) and 2 (interactive) cannot carry this envelope. Older
guests reject the request version before dispatch, even if the application image
contains an executable with the reserved name. It carries the
run and deployment identity, argv, explicit environment overrides and bounded
regular-file inputs. Existing argument limits remain in force; JSON is split
across UTF-8-safe argument boundaries. Older guests fail rather than executing a
host fallback. The envelope hash binds returned artifacts to the submitted
inputs. Input bytes and explicit environment values are retained as task intent;
they must not contain credentials. Deployment-scoped environment and secrets
remain governed by the existing app-task contract.

The first protocol supports 32 files totaling 8 KiB, a 15 KiB envelope, 8 KiB of
combined command output, 24 KiB of behavior JSON, 4 KiB of trigger values and a
64 KiB receipt. These are evidence framing bounds, not account quotas. Inputs
are installed beneath the app working directory with symlink traversal refused.
Output paths are private, bounded regular files. Receipts remain
application-reported observations; they do not establish database or object
storage consistency.

Admission checks retained simulation/containment enrollment, the immutable
deployment pin, and the open recorder window. A command task is a supported
recorder execution identity; ordinary tasks and standalone Runs remain refused
for contained apps. A command's recorder first stops new relay requests and
joins handlers. Only confirmed physical VM cleanup permits a retirement receipt.
Failed cleanup, missing output, pending obligations, cancellation, timeout and
lost owners cannot produce complete command evidence. Sealing includes command
tasks, including tasks admitted before a VM recorder could be opened.

Scheduler-owned command network rows bind the restoring task lease, node and
source address after runtime acknowledgement and before dispatch. Fresh service
caller reads include these rows only while the task lease, recorder and run are
active. They never appear as serving endpoints. Retirement revokes this authority
before physical address reuse; a failed revocation retains the allocation. vmmd
startup revokes previous command identities for its node before admission. The
SQL fences preserve task intent, forbid interactive attachment, and require the
matching retired receipt before success.

The CLI opts in explicitly, requires command protocol acknowledgement before
deploying, and rejects unsupported recipe combinations before provisioning.
Cleanup commands execute before action sealing. Arbitrary detached application
work is terminated with the command VM; recipes must declare completion of work
delegated to supported serving workers. General filesystem persistence between
commands, database/object collectors and lifecycle fault matrices remain later
increments.

Keep `FAAS_REHEARSAL_COMMAND_EXECUTION`,
`FAAS_REHEARSAL_NETWORK_CONTAINMENT` and
`FAAS_REHEARSAL_ACTION_SIMULATION` unset until dedicated native x86_64 KVM
qualification covers command boot, inputs, artifacts, cancellation, background
children, recorder failure, cleanup failure and leakcheck. Upgrade guest-init,
schedd, vmmd, outboundd and apid together.
