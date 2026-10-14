# Deployment Companion Read Model Implementation Plan

Current-base continuation (October15): the original reviewed implementation in #4638 passed CI, then conflicted with newer main. This branch preserves its behavior on immutable `c70cf7689d54f61f162159ebbc102a29f31aa542`; only AST-source and generated Python import lists needed conflict resolution, retaining both additions. The old numeric server ratchet was removed by newer main; this change raises no budget and still shrinks the server facade. Generated Node/Python clients are included. No PR or default branch is merged or force-pushed.

> **For agentic workers:** Use superpowers:executing-plans inline. Steps use checkboxes for tracking.

**Goal:** Let the console inspect actual declared deployment companions without revealing environment values or inventing health.

**Architecture:** Add an owned read-only deployment endpoint in `pkg/apid/deploymentread`. Lift the existing deployment GET ownership/serialization flow into that domain as a cohesive shared read boundary, retaining thin server routing. Project persisted Sidecars into a bounded public DTO; no runtime probes or decryption.

**Tech Stack:** Go, existing state.Store/MemStore fixtures, mirrored OpenAPI and Go SDK. Frontend React/TanStack consumers ship separately.

**Spec:** Approved console coverage plan L1; faas-web#155 and faas#4633. Backend pin `15a8415ea48821b71e8551cb8a19ca0d0f61dd5e`. Frontend independent branch based on J `26e6234aa3d3d40281529684fc8cba86421d208c`.

## Global constraints

- Both dirty main checkouts remain untouched; existing isolated backend worktree, branch `feat/deployment-companion-read-model`. Inline implementation and bounded dependent PRs, no merges or live traffic.
- Never return environment values, sealed ciphertext, secret refs, command strings or unknown fields. Names and declared configuration are metadata; missing health stays Unverified.
- Account-owned read/MFA/deployment-read scope protections mirror existing GET deployment. A deployment ID is the environment identity; no fake stage query. Preserve existing deployment GET behavior.
- API-server growth gate remains enforced. New logic lives in the domain; shared deployment GET extraction offsets the concurrently merged seven-line server growth. Do not raise the size budget or remove tests to pass it.
- No preset/runtime/transport qualification claim and no probe/tool execution. Unavailable preset does not prove all custom helpers unavailable.

## Review focus

1. Foreign account deployment: 404 before parsing declarations or returning metadata.
2. Legacy empty/null declarations versus malformed retained metadata: known empty is available with zero helpers; malformed is unavailable, never falsely empty.
3. Malicious/unexpected secret-bearing fields: JSON response whitelist contains only declaration metadata, environment keys and identities.
4. Configured zero resources/inheritance versus runtime health: preserve configured values without reporting effective allocation or healthy execution.
5. Scope/deployment/context mismatch: preserve exact IDs/scope, MFA and read authorization; frontend stale responses cannot enter a new context.

## Task 1: Projection and read domain

**Files:** `pkg/api/deployment_companions.go` and tests; `pkg/apid/deploymentread/companions.go`, `handlers.go`, corresponding tests; thin server GET/route adapter.

**Interfaces:** GET `/v1/deployments/{id}/companions` returns `DeploymentCompanionsResponse{AccountID,AppID,AppSlug,DeploymentID,Scope,State,Reason,Companions}`. State is `available|unavailable`; helpers carry name/image/preset/type, port/primary_ingress, declared memory/CPU/scratch/disk policy, essential and dependencies plus environment key names only. No health payload is invented. A known empty declaration is an available empty array. Maximum is actual `api.SidecarCapMax=5`; dependencies actual `WorkloadDependencyCapMax=6`.

- [ ] RED projection/handler tests: owned record, foreign404, zero/null/malformed declarations, injected raw/sealed environment values/commands/unexpected tokens, sorted environment keys, resources and dependencies, shared original GET behavior.

```go
raw := json.RawMessage(`[{"name":"metrics","type":"sidecar","env":{"TOKEN":"never-return"},"cmd":["never-return"]}]`)
view := projectCompanions(raw)
encoded, _ := json.Marshal(view)
if bytes.Contains(encoded, []byte("never-return")) { t.Fatal("credential disclosure") }
```

- [ ] Implement strict bounded projection, preserving only safe pinned images and declared metadata. Return generic unavailable reason for malformed metadata without including raw content.
- [ ] Implement domain owned deployment loading; original GET uses the same legacy404 behavior/serialization callback. New companion GET distinguishes temporary read failure503 and missing/foreign404, rejects unsupported query scope. Authenticate/MFA/scope in the actual router before domain invocation.
- [ ] GREEN domain plus actual routed security/legacy deployment tests; size guard, build/vet and diff checks. Atomic projection/client and domain/wiring commits where independently meaningful.

## Task 2: API/SDK qualification and delivery

**Files:** `api/openapi.yaml`, mirrored `pkg/apid/openapi.yaml`, Go SDK tests, SDK coverage aliases and AST DTO source list; `docs/companions.md` bounded endpoint description.

**Interface:** `Client.GetDeploymentCompanions(ctx,id)` uses GET with escaped exact ID, no probe or mutation; returns the typed whitelisted DTO.

- [ ] SDK request/abort tests RED then GREEN; add response schemas with identity/state bounds and no secret fields, mirror spec and map SDK/AST coverage.
- [ ] Verify OpenAPI lint/spec parity, meaningful existing deployment-read/security tests, domain build/vet and full affected gates; record any unchanged failing baseline gates explicitly.
- [ ] One fresh GPT-6.1 Sol/High final reviewer, fix Important/Critical findings RED→GREEN; push/open bounded backend PR based on main, attach and update #4633/#155/#134. No merge.

## Task 3: Separate frontend companion journey

**Files:** frontend `src/lib/api/companions.ts`, `src/components/dashboard/companions-summary.tsx`, tests; Configuration/Delivery integration, capability destination and mock fixtures.

- [ ] Pin only this new contract slice; account/app/deployment-scoped abortable reads and sanitized deployment choices. No raw companion declaration or credential enters caches.
- [ ] RED tests for selected exact deployment/scope, absent health, configured resources/env key names, empty/unavailable metadata, missing read contract, capability/preset failure and delayed account/app changes.
- [ ] Show actual declarations with Unverified execution health and manifest/CLI handoff. Preserve existing source, image, policy and app journeys.
- [ ] Focused GREEN/type/lint/format, atomic UI/behavior commits; full check/build, fixture-only mobile/history/keyboard/context/failure walkthrough. One fresh Sol6.1/High final review, bounded frontend PR based on J dependent on backend; attach/update trackers/handoff honestly.

## Self-review

All L1 requirements map to these tasks. The new safe read contract is necessary to implement the approved journey and is authorized by the user's explicit remaining-work request. The plan records execution details; it does not request new product/staging authority. Preset availability and native runtime qualification remain separate evidence.
