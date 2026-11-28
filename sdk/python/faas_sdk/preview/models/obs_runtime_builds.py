from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_builds_expected_role import (
    ObsRuntimeBuildsExpectedRole,
    check_obs_runtime_builds_expected_role,
)
from ..models.obs_runtime_builds_max_age_seconds import (
    ObsRuntimeBuildsMaxAgeSeconds,
    check_obs_runtime_builds_max_age_seconds,
)
from ..models.obs_runtime_builds_scope import ObsRuntimeBuildsScope, check_obs_runtime_builds_scope
from ..models.obs_runtime_builds_state import ObsRuntimeBuildsState, check_obs_runtime_builds_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_health import ObsRuntimeAppTaskHealth
    from ..models.obs_runtime_app_task_operations import ObsRuntimeAppTaskOperations
    from ..models.obs_runtime_app_task_results import ObsRuntimeAppTaskResults
    from ..models.obs_runtime_build_counts import ObsRuntimeBuildCounts
    from ..models.obs_runtime_build_item import ObsRuntimeBuildItem
    from ..models.obs_runtime_build_job import ObsRuntimeBuildJob
    from ..models.obs_runtime_build_recovery_health import ObsRuntimeBuildRecoveryHealth
    from ..models.obs_runtime_builder_health import ObsRuntimeBuilderHealth
    from ..models.obs_runtime_command_cron_health import ObsRuntimeCommandCronHealth
    from ..models.obs_runtime_command_cron_outcomes import ObsRuntimeCommandCronOutcomes
    from ..models.obs_runtime_command_cron_timing import ObsRuntimeCommandCronTiming
    from ..models.obs_runtime_delayed_task_dispatch import ObsRuntimeDelayedTaskDispatch
    from ..models.obs_runtime_diagnostics import ObsRuntimeDiagnostics
    from ..models.obs_runtime_handoff_health import ObsRuntimeHandoffHealth
    from ..models.obs_runtime_http_cron_delivery import ObsRuntimeHTTPCronDelivery
    from ..models.obs_runtime_loop_health import ObsRuntimeLoopHealth
    from ..models.obs_runtime_post_capture_health import ObsRuntimePostCaptureHealth
    from ..models.obs_runtime_preparation_health import ObsRuntimePreparationHealth
    from ..models.obs_runtime_process import ObsRuntimeProcess
    from ..models.obs_runtime_readiness import ObsRuntimeReadiness
    from ..models.obs_runtime_snapshot_prime_health import ObsRuntimeSnapshotPrimeHealth


T = TypeVar("T", bound="ObsRuntimeBuilds")


@_attrs_define
class ObsRuntimeBuilds:
    """Advisory sampled identity evidence; full-set counts and a bounded item preview."""

    generated_at: datetime.datetime
    scope: ObsRuntimeBuildsScope
    expected_role: ObsRuntimeBuildsExpectedRole
    requested_release: str
    evidence_after: datetime.datetime
    state: ObsRuntimeBuildsState
    max_age_seconds: ObsRuntimeBuildsMaxAgeSeconds
    counts: ObsRuntimeBuildCounts
    """State counts partition all inspected items; expected, observed, unexpected and distinct_revisions overlap
    these counts. Unavailable jobs can have unknown expected counts; inspect jobs."""
    jobs: list[ObsRuntimeBuildJob]
    items: list[ObsRuntimeBuildItem]
    omitted_items: int
    limitations: list[str]
    reason: str | Unset = UNSET
    readiness: ObsRuntimeReadiness | Unset = UNSET
    """Advisory readiness for the same inspected targets; independent of revision matching and qualification.
    Omitted by older servers."""
    process: ObsRuntimeProcess | Unset = UNSET
    """Advisory sampled process continuity at two evaluation times; an observed change does not prove a crash or
    count every restart between samples."""
    loop_health: ObsRuntimeLoopHealth | Unset = UNSET
    """Advisory reports from expected runtime and work loops; fresh reported stalls remain visible when age
    evidence is incomplete. Counts cover all inspected target-loop pairs before the bounded preview."""
    builder_health: ObsRuntimeBuilderHealth | Unset = UNSET
    """Advisory build worker progress and process-local queue/slot pressure for explicit v2 profiles. Counts cover
    every inspected target before previewing. A fresh overdue worker remains visible with incomplete queue/slot
    evidence; saturation does not establish a build failure."""
    handoff_health: ObsRuntimeHandoffHealth | Unset = UNSET
    """Advisory build-to-image handoff evidence for selected image workers in v2 profiles. Replica recovery scopes
    can overlap; pending values are never summed into a fleet total. Positive waiting evidence survives incomplete
    recovery metadata."""
    preparation_health: ObsRuntimePreparationHealth | Unset = UNSET
    """Advisory durable image-preparation evidence for image workers in v2 profiles. Counts describe observation
    scopes, which may overlap across replicas. Positive phase evidence survives gaps in other components. No
    execution deadline or deployment success is inferred."""
    snapshot_prime_health: ObsRuntimeSnapshotPrimeHealth | Unset = UNSET
    """Advisory snapshot-priming evidence for scheduler jobs selected by v2 profiles. Ownership scopes may overlap;
    never sum attempt counts across replicas. Active or recorded snapshots do not establish deployment readiness.
   """
    command_cron_outcomes: ObsRuntimeCommandCronOutcomes | Unset = UNSET
    """Advisory process-local cron sweep, scheduling-decision and admission-return counters for v2 scheduler jobs.
    The 29 fixed source/outcome signals are independent. Sweep success covers a shared scan including inline HTTP
    work and does not prove every command was admitted. Decisions are not unique due occurrences; queued,
    exclusive_pending and coalesced returns do not prove command execution or delivery. Baselines, changed
    processes, resets, stale or missing evidence establish no delta. Exact differences require unchanged sampled
    process proof, matching original scrape clocks and uptime advance within the existing one-second tolerance.
    Sampled continuity cannot exclude every intervening restart. No scheduling policy, rate or deployment gate is
    inferred."""
    command_cron_timing: ObsRuntimeCommandCronTiming | Unset = UNSET
    """Advisory process-local command-cron admission timing for v2 scheduler jobs. Timed observations measure
    returned occurrence CreatedAt minus nominal ScheduledFor, including catch-up history and policy dispositions.
    This is not database commit time, execution-start or continuous eligibility latency. The eleven cumulative
    histogram buckets and count require complete coherent snapshots and coherent interval distributions. The
    unknown-timestamp counter is independent. Exact differences require unchanged sampled process proof, fresh
    matching original scrape clocks and uptime advance within the existing one-second tolerance. Baselines, missing
    evidence, restarts and resets establish no timing delta. No observations does not establish zero lag. No
    percentile, unique task count or deployment gate is inferred."""
    diagnostics: ObsRuntimeDiagnostics | Unset = UNSET
    """Explicit source-side selection acknowledgement. Absent on default requests to preserve compatibility.
    Selected and not_selected are disjoint canonical subsequences partitioning the groups supported by the role. A
    selected group can still be unavailable; a not_selected group is intentionally omitted. Selection never
    suppresses core identity, readiness, process or loop evidence, expands a budget, or changes a deployment gate.
   """
    http_cron_delivery: ObsRuntimeHTTPCronDelivery | Unset = UNSET
    """Advisory settled HTTP cron delivery attempts for v2 scheduler jobs, including scheduled fires and manual
    runs. The five ordered outcomes are success, retry, app_error, failed and dead_letter. Application errors remain
    separate from producer-classified delivery failures. Retry does not establish its cause or final result, and
    success does not prove unique or exactly-once execution. Exact counter differences require fresh original sample
    clocks, unchanged sampled process proof and coherent uptime advance within the existing one-second tolerance.
    Baselines, resets, process changes and incomplete evidence establish no delta. No deployment gate or failure-
    rate threshold is inferred."""
    delayed_task_dispatch: ObsRuntimeDelayedTaskDispatch | Unset = UNSET
    """Advisory settled delayed-task dispatch attempt counters for v2 scheduler jobs. The four ordered outcomes are
    success, retry, failed and dead_letter. Failed includes application errors and does not identify a platform
    incident. Retry does not establish its cause or final result, and success does not prove unique or exactly-once
    execution. Exact differences require fresh original sample clocks, unchanged sampled process proof and coherent
    uptime advance within the existing one-second tolerance. Baselines, resets, changed processes and incomplete
    evidence establish no delta. No deployment gate or failure-rate threshold is inferred."""
    app_task_results: ObsRuntimeAppTaskResults | Unset = UNSET
    """Advisory sampled completion acknowledgements and recovery sweep changes for v2 scheduler processes.
    Completion uses the returned durable status by release, manual or cron kind; queued means a policy retry.
    Recovery reports only requeued_restores, failed_runs and cancelled from successful sweeps. Failed running tasks
    can have uncertain command outcomes. These are process-local counter observations, not unique tasks, delivery
    acknowledgements, infrastructure fault rates or deployment gates. Baselines, resets, changed processes, stale or
    missing samples establish no delta. Comparisons require unchanged sampled process proof, matching original
    scrape clocks and uptime advance within the existing one-second tolerance. Sampled continuity cannot rule out
    every intervening restart."""
    app_task_operations: ObsRuntimeAppTaskOperations | Unset = UNSET
    """Advisory observed operation counter changes for v2 scheduler jobs. Counters belong to individual processes.
    Five operations and seven outcomes are inspected without treating missing capacity series as zero. First samples
    are baselines, not newly observed failures. Deltas require unchanged sampled process identity, fresh points at
    both pinned times and matching original scrape clocks. Uptime advance must match elapsed scrape time within the
    existing one-second reset tolerance. Sampled continuity cannot rule out every intervening restart. These are
    observed sample differences, not rates, exact event times or command verdicts."""
    app_task_health: ObsRuntimeAppTaskHealth | Unset = UNSET
    """Advisory release, manual and cron task evidence for v2 scheduler jobs. Each scheduler observes the shared
    fleet independently. Compare views without summing task counts. Local worker slots do not measure fleet tasks or
    VMs. No deployment health or eligibility verdict is inferred."""
    command_cron_health: ObsRuntimeCommandCronHealth | Unset = UNSET
    """Advisory command-cron schedule and sweep evidence for v2 scheduler jobs. Ownership scopes may overlap;
    compare scopes without summing schedules. Nonempty includes intentional inactive schedules. Due evidence
    describes an unconsumed nominal boundary, not a health, eligibility, admission or execution verdict."""
    post_capture_health: ObsRuntimePostCaptureHealth | Unset = UNSET
    """Advisory post-capture activation evidence for image workers in v2 profiles. Each replica reads the shared
    fleet independently; compare its view without summing deployment counts or inferring consensus. Gate
    classifications and ages are retained evidence, not a new eligibility or health verdict."""
    build_recovery_health: ObsRuntimeBuildRecoveryHealth | Unset = UNSET
    """Advisory running build claims and durable VM cleanup obligations as independently collected API replica
    views of the shared fleet. Compare views without summing build or cleanup counts. Cleanup age begins at
    obligation creation and survives retries; evidence is not a VM liveness or deployment-readiness verdict."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        scope: str = self.scope

        expected_role: str = self.expected_role

        requested_release = self.requested_release

        evidence_after = self.evidence_after.isoformat()

        state: str = self.state

        max_age_seconds: int = self.max_age_seconds

        counts = self.counts.to_dict()

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        omitted_items = self.omitted_items

        limitations = self.limitations

        reason = self.reason

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        process: dict[str, Any] | Unset = UNSET
        if not isinstance(self.process, Unset):
            process = self.process.to_dict()

        loop_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.loop_health, Unset):
            loop_health = self.loop_health.to_dict()

        builder_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.builder_health, Unset):
            builder_health = self.builder_health.to_dict()

        handoff_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.handoff_health, Unset):
            handoff_health = self.handoff_health.to_dict()

        preparation_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.preparation_health, Unset):
            preparation_health = self.preparation_health.to_dict()

        snapshot_prime_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.snapshot_prime_health, Unset):
            snapshot_prime_health = self.snapshot_prime_health.to_dict()

        command_cron_outcomes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.command_cron_outcomes, Unset):
            command_cron_outcomes = self.command_cron_outcomes.to_dict()

        command_cron_timing: dict[str, Any] | Unset = UNSET
        if not isinstance(self.command_cron_timing, Unset):
            command_cron_timing = self.command_cron_timing.to_dict()

        diagnostics: dict[str, Any] | Unset = UNSET
        if not isinstance(self.diagnostics, Unset):
            diagnostics = self.diagnostics.to_dict()

        http_cron_delivery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.http_cron_delivery, Unset):
            http_cron_delivery = self.http_cron_delivery.to_dict()

        delayed_task_dispatch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.delayed_task_dispatch, Unset):
            delayed_task_dispatch = self.delayed_task_dispatch.to_dict()

        app_task_results: dict[str, Any] | Unset = UNSET
        if not isinstance(self.app_task_results, Unset):
            app_task_results = self.app_task_results.to_dict()

        app_task_operations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.app_task_operations, Unset):
            app_task_operations = self.app_task_operations.to_dict()

        app_task_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.app_task_health, Unset):
            app_task_health = self.app_task_health.to_dict()

        command_cron_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.command_cron_health, Unset):
            command_cron_health = self.command_cron_health.to_dict()

        post_capture_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.post_capture_health, Unset):
            post_capture_health = self.post_capture_health.to_dict()

        build_recovery_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.build_recovery_health, Unset):
            build_recovery_health = self.build_recovery_health.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "scope": scope,
                "expected_role": expected_role,
                "requested_release": requested_release,
                "evidence_after": evidence_after,
                "state": state,
                "max_age_seconds": max_age_seconds,
                "counts": counts,
                "jobs": jobs,
                "items": items,
                "omitted_items": omitted_items,
                "limitations": limitations,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if process is not UNSET:
            field_dict["process"] = process
        if loop_health is not UNSET:
            field_dict["loop_health"] = loop_health
        if builder_health is not UNSET:
            field_dict["builder_health"] = builder_health
        if handoff_health is not UNSET:
            field_dict["handoff_health"] = handoff_health
        if preparation_health is not UNSET:
            field_dict["preparation_health"] = preparation_health
        if snapshot_prime_health is not UNSET:
            field_dict["snapshot_prime_health"] = snapshot_prime_health
        if command_cron_outcomes is not UNSET:
            field_dict["command_cron_outcomes"] = command_cron_outcomes
        if command_cron_timing is not UNSET:
            field_dict["command_cron_timing"] = command_cron_timing
        if diagnostics is not UNSET:
            field_dict["diagnostics"] = diagnostics
        if http_cron_delivery is not UNSET:
            field_dict["http_cron_delivery"] = http_cron_delivery
        if delayed_task_dispatch is not UNSET:
            field_dict["delayed_task_dispatch"] = delayed_task_dispatch
        if app_task_results is not UNSET:
            field_dict["app_task_results"] = app_task_results
        if app_task_operations is not UNSET:
            field_dict["app_task_operations"] = app_task_operations
        if app_task_health is not UNSET:
            field_dict["app_task_health"] = app_task_health
        if command_cron_health is not UNSET:
            field_dict["command_cron_health"] = command_cron_health
        if post_capture_health is not UNSET:
            field_dict["post_capture_health"] = post_capture_health
        if build_recovery_health is not UNSET:
            field_dict["build_recovery_health"] = build_recovery_health

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_app_task_health import ObsRuntimeAppTaskHealth
        from ..models.obs_runtime_app_task_operations import ObsRuntimeAppTaskOperations
        from ..models.obs_runtime_app_task_results import ObsRuntimeAppTaskResults
        from ..models.obs_runtime_build_counts import ObsRuntimeBuildCounts
        from ..models.obs_runtime_build_item import ObsRuntimeBuildItem
        from ..models.obs_runtime_build_job import ObsRuntimeBuildJob
        from ..models.obs_runtime_build_recovery_health import ObsRuntimeBuildRecoveryHealth
        from ..models.obs_runtime_builder_health import ObsRuntimeBuilderHealth
        from ..models.obs_runtime_command_cron_health import ObsRuntimeCommandCronHealth
        from ..models.obs_runtime_command_cron_outcomes import ObsRuntimeCommandCronOutcomes
        from ..models.obs_runtime_command_cron_timing import ObsRuntimeCommandCronTiming
        from ..models.obs_runtime_delayed_task_dispatch import ObsRuntimeDelayedTaskDispatch
        from ..models.obs_runtime_diagnostics import ObsRuntimeDiagnostics
        from ..models.obs_runtime_handoff_health import ObsRuntimeHandoffHealth
        from ..models.obs_runtime_http_cron_delivery import ObsRuntimeHTTPCronDelivery
        from ..models.obs_runtime_loop_health import ObsRuntimeLoopHealth
        from ..models.obs_runtime_post_capture_health import ObsRuntimePostCaptureHealth
        from ..models.obs_runtime_preparation_health import ObsRuntimePreparationHealth
        from ..models.obs_runtime_process import ObsRuntimeProcess
        from ..models.obs_runtime_readiness import ObsRuntimeReadiness
        from ..models.obs_runtime_snapshot_prime_health import ObsRuntimeSnapshotPrimeHealth

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        scope = check_obs_runtime_builds_scope(d.pop("scope"))

        expected_role = check_obs_runtime_builds_expected_role(d.pop("expected_role"))

        requested_release = d.pop("requested_release")

        evidence_after = datetime.datetime.fromisoformat(d.pop("evidence_after"))

        state = check_obs_runtime_builds_state(d.pop("state"))

        max_age_seconds = check_obs_runtime_builds_max_age_seconds(d.pop("max_age_seconds"))

        counts = ObsRuntimeBuildCounts.from_dict(d.pop("counts"))

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = ObsRuntimeBuildJob.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeBuildItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        limitations = cast(list[str], d.pop("limitations"))

        reason = d.pop("reason", UNSET)

        _readiness = d.pop("readiness", UNSET)
        readiness: ObsRuntimeReadiness | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = ObsRuntimeReadiness.from_dict(_readiness)

        _process = d.pop("process", UNSET)
        process: ObsRuntimeProcess | Unset
        if isinstance(_process, Unset):
            process = UNSET
        else:
            process = ObsRuntimeProcess.from_dict(_process)

        _loop_health = d.pop("loop_health", UNSET)
        loop_health: ObsRuntimeLoopHealth | Unset
        if isinstance(_loop_health, Unset):
            loop_health = UNSET
        else:
            loop_health = ObsRuntimeLoopHealth.from_dict(_loop_health)

        _builder_health = d.pop("builder_health", UNSET)
        builder_health: ObsRuntimeBuilderHealth | Unset
        if isinstance(_builder_health, Unset):
            builder_health = UNSET
        else:
            builder_health = ObsRuntimeBuilderHealth.from_dict(_builder_health)

        _handoff_health = d.pop("handoff_health", UNSET)
        handoff_health: ObsRuntimeHandoffHealth | Unset
        if isinstance(_handoff_health, Unset):
            handoff_health = UNSET
        else:
            handoff_health = ObsRuntimeHandoffHealth.from_dict(_handoff_health)

        _preparation_health = d.pop("preparation_health", UNSET)
        preparation_health: ObsRuntimePreparationHealth | Unset
        if isinstance(_preparation_health, Unset):
            preparation_health = UNSET
        else:
            preparation_health = ObsRuntimePreparationHealth.from_dict(_preparation_health)

        _snapshot_prime_health = d.pop("snapshot_prime_health", UNSET)
        snapshot_prime_health: ObsRuntimeSnapshotPrimeHealth | Unset
        if isinstance(_snapshot_prime_health, Unset):
            snapshot_prime_health = UNSET
        else:
            snapshot_prime_health = ObsRuntimeSnapshotPrimeHealth.from_dict(_snapshot_prime_health)

        _command_cron_outcomes = d.pop("command_cron_outcomes", UNSET)
        command_cron_outcomes: ObsRuntimeCommandCronOutcomes | Unset
        if isinstance(_command_cron_outcomes, Unset):
            command_cron_outcomes = UNSET
        else:
            command_cron_outcomes = ObsRuntimeCommandCronOutcomes.from_dict(_command_cron_outcomes)

        _command_cron_timing = d.pop("command_cron_timing", UNSET)
        command_cron_timing: ObsRuntimeCommandCronTiming | Unset
        if isinstance(_command_cron_timing, Unset):
            command_cron_timing = UNSET
        else:
            command_cron_timing = ObsRuntimeCommandCronTiming.from_dict(_command_cron_timing)

        _diagnostics = d.pop("diagnostics", UNSET)
        diagnostics: ObsRuntimeDiagnostics | Unset
        if isinstance(_diagnostics, Unset):
            diagnostics = UNSET
        else:
            diagnostics = ObsRuntimeDiagnostics.from_dict(_diagnostics)

        _http_cron_delivery = d.pop("http_cron_delivery", UNSET)
        http_cron_delivery: ObsRuntimeHTTPCronDelivery | Unset
        if isinstance(_http_cron_delivery, Unset):
            http_cron_delivery = UNSET
        else:
            http_cron_delivery = ObsRuntimeHTTPCronDelivery.from_dict(_http_cron_delivery)

        _delayed_task_dispatch = d.pop("delayed_task_dispatch", UNSET)
        delayed_task_dispatch: ObsRuntimeDelayedTaskDispatch | Unset
        if isinstance(_delayed_task_dispatch, Unset):
            delayed_task_dispatch = UNSET
        else:
            delayed_task_dispatch = ObsRuntimeDelayedTaskDispatch.from_dict(_delayed_task_dispatch)

        _app_task_results = d.pop("app_task_results", UNSET)
        app_task_results: ObsRuntimeAppTaskResults | Unset
        if isinstance(_app_task_results, Unset):
            app_task_results = UNSET
        else:
            app_task_results = ObsRuntimeAppTaskResults.from_dict(_app_task_results)

        _app_task_operations = d.pop("app_task_operations", UNSET)
        app_task_operations: ObsRuntimeAppTaskOperations | Unset
        if isinstance(_app_task_operations, Unset):
            app_task_operations = UNSET
        else:
            app_task_operations = ObsRuntimeAppTaskOperations.from_dict(_app_task_operations)

        _app_task_health = d.pop("app_task_health", UNSET)
        app_task_health: ObsRuntimeAppTaskHealth | Unset
        if isinstance(_app_task_health, Unset):
            app_task_health = UNSET
        else:
            app_task_health = ObsRuntimeAppTaskHealth.from_dict(_app_task_health)

        _command_cron_health = d.pop("command_cron_health", UNSET)
        command_cron_health: ObsRuntimeCommandCronHealth | Unset
        if isinstance(_command_cron_health, Unset):
            command_cron_health = UNSET
        else:
            command_cron_health = ObsRuntimeCommandCronHealth.from_dict(_command_cron_health)

        _post_capture_health = d.pop("post_capture_health", UNSET)
        post_capture_health: ObsRuntimePostCaptureHealth | Unset
        if isinstance(_post_capture_health, Unset):
            post_capture_health = UNSET
        else:
            post_capture_health = ObsRuntimePostCaptureHealth.from_dict(_post_capture_health)

        _build_recovery_health = d.pop("build_recovery_health", UNSET)
        build_recovery_health: ObsRuntimeBuildRecoveryHealth | Unset
        if isinstance(_build_recovery_health, Unset):
            build_recovery_health = UNSET
        else:
            build_recovery_health = ObsRuntimeBuildRecoveryHealth.from_dict(_build_recovery_health)

        obs_runtime_builds = cls(
            generated_at=generated_at,
            scope=scope,
            expected_role=expected_role,
            requested_release=requested_release,
            evidence_after=evidence_after,
            state=state,
            max_age_seconds=max_age_seconds,
            counts=counts,
            jobs=jobs,
            items=items,
            omitted_items=omitted_items,
            limitations=limitations,
            reason=reason,
            readiness=readiness,
            process=process,
            loop_health=loop_health,
            builder_health=builder_health,
            handoff_health=handoff_health,
            preparation_health=preparation_health,
            snapshot_prime_health=snapshot_prime_health,
            command_cron_outcomes=command_cron_outcomes,
            command_cron_timing=command_cron_timing,
            diagnostics=diagnostics,
            http_cron_delivery=http_cron_delivery,
            delayed_task_dispatch=delayed_task_dispatch,
            app_task_results=app_task_results,
            app_task_operations=app_task_operations,
            app_task_health=app_task_health,
            command_cron_health=command_cron_health,
            post_capture_health=post_capture_health,
            build_recovery_health=build_recovery_health,
        )

        obs_runtime_builds.additional_properties = d
        return obs_runtime_builds

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
