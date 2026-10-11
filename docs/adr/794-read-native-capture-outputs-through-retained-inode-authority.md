# ADR-794: Read native capture outputs through retained inode authority

Date: 2026-10-11
Status: Implemented; focused Linux race validation passed. Native acceptance pending.

## Context

Native capture stages anonymous memory and VM state files under durable image
ownership. The legacy snapshot publisher opens or moves jail paths and cannot
consume those files without losing the original output identity.

## Decision

Add a synchronous output reader for the original capture. Require the current
local daemon producer, incoming capture and live physical process. Retain the
physical and source locks until the consumer returns and its read-only,
close-on-exec descriptor closes. Resolve the capture-specific name to exactly
one exclusive ready source epoch; open its retained anchor instead of the jail
path. Refuse aliases, replacement inodes, stale captures and empty outputs.

The capture adapter remains responsible for pausing, completing snapshot/create,
freezing the private drive and fencing object publication. This reader does not
enable the unfinished native capture adapter or regional serving admission.

## Validation

Portable fixtures exercise original bytes, path replacement, capture and process
ownership, ambiguous references, descriptor cleanup and lock lifetime. Linux
checks exercise the read-only descriptor requirements and binding refusals.
They do not establish native mount, capture, recovery or serving acceptance.

The selected capture output, input and frozen-drive race suites pass all 125
test events on the internal Linux node, including the new output reader and
Linux O_PATH refusal. The node uses nested GCE virtualization and remains a
diagnostic environment; dedicated native KVM qualification is still required.
