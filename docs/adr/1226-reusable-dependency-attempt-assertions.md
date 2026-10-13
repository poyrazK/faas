# 1018: Reuse dependency-attempt limits in scenario assertion profiles

Date: 2026-10-13

Status: Accepted

## Context

Named healthy-route and recovery profiles let users reuse application
requirements. Retry storms require additional limits on outgoing dependency
traffic. HTTP calls and TCP connections have different counters and cannot be
inferred from one another or from application status codes.

## Decision

Extend assertion profiles with `dependency_attempts` and `connection_attempts`.
Each selects a named incoming request, a finite positive maximum average
attempts/request ratio and a positive minimum incoming sample count. Compile
these into the existing HTTP retry-amplification and TCP connection-attempt
assertions with independently copied budgets and unchanged evaluators.

Share static attempt-budget validation with the inline assertion forms. Keep
phase-specific validation: one full-coverage rule, explicit source workload,
HTTP latency/status or TCP kind and port as appropriate. Reject incompatible
mixed profiles and any budget collision instead of silently overriding limits.

Use complete managed proxy evidence. TCP must use its dedicated attempt counter,
including pre-data failures. Missing counters, incomplete capture and insufficient
incoming samples fail. Existing JSON, HTML, JUnit and terminal evidence records
observed counts, ratio and limit, alongside the referenced profile name. Replay
fingerprints bind effective limits.

## Consequences

This is an average over a fault phase, not tracing of each logical request.
Dependency counters include all traffic on the selected source/target rule;
other incoming routes do not dilute the named request denominator. The feature
changes no application retry policy or platform gateway default. The reference
manifests and portable HTTP/TCP tests qualify configuration and evaluation;
native execution still requires the designated KVM fleet.
