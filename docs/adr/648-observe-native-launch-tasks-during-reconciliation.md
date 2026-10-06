# ADR-648: Observe root launch tasks throughout regional reconciliation

Date: 2026-10-07
Status: Implementation in progress; complete inventory and native acceptance pending.

## Context

A fresh native runtime inventory observes managed jail UIDs, retained VM owners
and tenant cgroups. A root jailer or vmmd pre-exec launch helper can still exist
outside the jail UID range. Such a task can exec, disappear or change its target
between the initial recovery scan and the inventory consumer. The producer
barrier cannot freeze these historical kernel tasks.

## Decision

Each fresh runtime inventory independently discovers native launch tasks twice
and requires equal PID, start time, observed UID, intended jail UID, instance,
plan, builder scope and launcher kind. Process directory spellings must be
canonical and fit Linux PID bounds. Each process scan checks that its procfs root
retains the original inode. An incomplete or changing read refuses the sample.

The inventory repeats this stable launch-task observation before and after its
synchronous consumer, alongside existing machine, cgroup, journal and managed-UID
checks. Root jailers and wrapped vmmd launch helpers are visible in the advisory
payload. A launcher remains unresolved even when it names an existing VM or
targets the same jail UID; it cannot merge into that VM's capacity hold. An
unmatched Firecracker process is unresolved as well. Payloads clone the task list
so consumer edits cannot rewrite the retained comparison.

This discovers recognizable native launch commands. It does not claim complete
inventory of arbitrary escaped historical root descendants or external writers,
and argv is not authenticated executable identity. Observations grant no current
producer/run/task authority, native stop evidence, capacity release or activation.
Every regional admission head stays frozen. Snapshot input shape, capture memory
authority, full capacity reconciliation, shared authorization/global billing,
independent regional stores/provisioning and cached global ingress remain part of
the end-to-end feature. Routing defaults to every selected healthy eligible
region. Dedicated native Linux x86_64 KVM metal, leakcheck and recovery acceptance
remain required; the nested internal GCE node supplies diagnostics only.

## Validation

Exercise root jailers and wrapped pre-exec helpers, duplicate instance names,
numeric PID ordering, task exit, exec, PID reuse, retargeting, plan changes,
unreadable processes, procfs-root replacement, noncanonical paths and cancellation.
Mutate tasks on both sides of the inventory consumer and require refusal without
signaling, waiting or capacity mutation. Verify stable launchers remain unresolved
and advisory-copy edits cannot escape. Run affected portable race tests and both
platform lint targets. Portable fixtures do not establish native acceptance.
