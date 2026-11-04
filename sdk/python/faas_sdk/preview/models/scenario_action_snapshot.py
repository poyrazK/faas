from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_action_snapshot_event_capture_version import (
    ScenarioActionSnapshotEventCaptureVersion,
    check_scenario_action_snapshot_event_capture_version,
)
from ..models.scenario_action_snapshot_platform_capture_version import (
    ScenarioActionSnapshotPlatformCaptureVersion,
    check_scenario_action_snapshot_platform_capture_version,
)
from ..models.scenario_action_snapshot_queue_capture_version import (
    ScenarioActionSnapshotQueueCaptureVersion,
    check_scenario_action_snapshot_queue_capture_version,
)
from ..models.scenario_action_snapshot_recorder_version import (
    ScenarioActionSnapshotRecorderVersion,
    check_scenario_action_snapshot_recorder_version,
)
from ..models.scenario_action_snapshot_scope import ScenarioActionSnapshotScope, check_scenario_action_snapshot_scope
from ..models.scenario_action_snapshot_simulation_version import (
    ScenarioActionSnapshotSimulationVersion,
    check_scenario_action_snapshot_simulation_version,
)
from ..models.scenario_action_snapshot_status import ScenarioActionSnapshotStatus, check_scenario_action_snapshot_status
from ..models.scenario_action_snapshot_version import (
    ScenarioActionSnapshotVersion,
    check_scenario_action_snapshot_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_captured_action import ScenarioCapturedAction
    from ..models.scenario_captured_event import ScenarioCapturedEvent
    from ..models.scenario_captured_platform_call import ScenarioCapturedPlatformCall
    from ..models.scenario_captured_queue_message import ScenarioCapturedQueueMessage
    from ..models.scenario_cold_start_evidence import ScenarioColdStartEvidence
    from ..models.scenario_managed_work_evidence import ScenarioManagedWorkEvidence
    from ..models.scenario_simulation_attempt import ScenarioSimulationAttempt
    from ..models.scenario_suppressed_webhook_events import ScenarioSuppressedWebhookEvents
    from ..models.scenario_suspend_resume_evidence import ScenarioSuspendResumeEvidence


T = TypeVar("T", bound="ScenarioActionSnapshot")


@_attrs_define
class ScenarioActionSnapshot:
    """Bounded evidence for a sealed action window. Version 1 covers managed webhooks. Version 2 adds declared simulated
    relay attempts and durable host recorder coverage. Incomplete snapshots never establish equivalent behavior.

    """

    version: ScenarioActionSnapshotVersion
    scope: ScenarioActionSnapshotScope
    run_id: str
    status: ScenarioActionSnapshotStatus
    workloads: list[str]
    intents: list[ScenarioCapturedAction]
    captured_count: int
    pending_managed_work: int
    incomplete_managed_effects: int
    suppressed_deliveries: int
    """Unsupported ordinary deliveries blocked by the run policy; these are not successful business effects."""
    truncated: bool
    suspend_resume: ScenarioSuspendResumeEvidence | Unset = UNSET
    """Versioned lifecycle inventory. Complete receipts preserve identity while comparisons exclude timing jitter;
    missing proof remains inconclusive."""
    cold_start: ScenarioColdStartEvidence | Unset = UNSET
    """Sealed cold-start inventory with immutable recipe identity and independent host receipts. Raw timing jitter
    and runtime IDs are excluded from behavioral equality."""
    managed_work: ScenarioManagedWorkEvidence | Unset = UNSET
    """Bounded participant inventory and managed async completion proof. A complete result requires worker
    execution, successful receipts and closed admission."""
    recorder_version: ScenarioActionSnapshotRecorderVersion | Unset = UNSET
    """Required in simulation scope. Durable host recorder and closing barrier contract."""
    recorder_sessions: int | Unset = UNSET
    """Retained serving-VM recorder sessions. Every simulation workload must have acknowledged recorder coverage."""
    pending_recorder_sessions: int | Unset = UNSET
    incomplete_recorder_sessions: int | Unset = UNSET
    sealed_at: datetime.datetime | Unset = UNSET
    """Present only after the managed-effect window has closed."""
    simulation_version: ScenarioActionSnapshotSimulationVersion | Unset = UNSET
    """Present in version 2 snapshots. HTTP and Resend simulation contract version."""
    simulation_sha256: str | Unset = UNSET
    """Canonical immutable fixture plan identity. Required for simulation scope."""
    simulated_attempts: list[ScenarioSimulationAttempt] | Unset = UNSET
    pending_simulation_attempts: int | Unset = UNSET
    incomplete_simulation_attempts: int | Unset = UNSET
    reason: str | Unset = UNSET
    event_capture_version: ScenarioActionSnapshotEventCaptureVersion | Unset = UNSET
    """Present from servers that capture published events. A comparison across capture versions is inconclusive."""
    published_events: list[ScenarioCapturedEvent] | Unset = UNSET
    suppressed_webhook_events: list[ScenarioSuppressedWebhookEvents] | Unset = UNSET
    incomplete_captured_events: int | Unset = UNSET
    queue_capture_version: ScenarioActionSnapshotQueueCaptureVersion | Unset = UNSET
    """Present from servers that capture queue sends and delayed tasks."""
    queue_messages: list[ScenarioCapturedQueueMessage] | Unset = UNSET
    incomplete_queue_messages: int | Unset = UNSET
    platform_capture_version: ScenarioActionSnapshotPlatformCaptureVersion | Unset = UNSET
    """Present from servers that capture workflow and realtime calls."""
    platform_calls: list[ScenarioCapturedPlatformCall] | Unset = UNSET
    incomplete_platform_calls: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        scope: str = self.scope

        run_id = self.run_id

        status: str = self.status

        workloads = self.workloads

        intents = []
        for intents_item_data in self.intents:
            intents_item = intents_item_data.to_dict()
            intents.append(intents_item)

        captured_count = self.captured_count

        pending_managed_work = self.pending_managed_work

        incomplete_managed_effects = self.incomplete_managed_effects

        suppressed_deliveries = self.suppressed_deliveries

        truncated = self.truncated

        suspend_resume: dict[str, Any] | Unset = UNSET
        if not isinstance(self.suspend_resume, Unset):
            suspend_resume = self.suspend_resume.to_dict()

        cold_start: dict[str, Any] | Unset = UNSET
        if not isinstance(self.cold_start, Unset):
            cold_start = self.cold_start.to_dict()

        managed_work: dict[str, Any] | Unset = UNSET
        if not isinstance(self.managed_work, Unset):
            managed_work = self.managed_work.to_dict()

        recorder_version: int | Unset = UNSET
        if not isinstance(self.recorder_version, Unset):
            recorder_version = self.recorder_version

        recorder_sessions = self.recorder_sessions

        pending_recorder_sessions = self.pending_recorder_sessions

        incomplete_recorder_sessions = self.incomplete_recorder_sessions

        sealed_at: str | Unset = UNSET
        if not isinstance(self.sealed_at, Unset):
            sealed_at = self.sealed_at.isoformat()

        simulation_version: int | Unset = UNSET
        if not isinstance(self.simulation_version, Unset):
            simulation_version = self.simulation_version

        simulation_sha256 = self.simulation_sha256

        simulated_attempts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.simulated_attempts, Unset):
            simulated_attempts = []
            for simulated_attempts_item_data in self.simulated_attempts:
                simulated_attempts_item = simulated_attempts_item_data.to_dict()
                simulated_attempts.append(simulated_attempts_item)

        pending_simulation_attempts = self.pending_simulation_attempts

        incomplete_simulation_attempts = self.incomplete_simulation_attempts

        reason = self.reason

        event_capture_version: int | Unset = UNSET
        if not isinstance(self.event_capture_version, Unset):
            event_capture_version = self.event_capture_version

        published_events: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.published_events, Unset):
            published_events = []
            for published_events_item_data in self.published_events:
                published_events_item = published_events_item_data.to_dict()
                published_events.append(published_events_item)

        suppressed_webhook_events: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.suppressed_webhook_events, Unset):
            suppressed_webhook_events = []
            for suppressed_webhook_events_item_data in self.suppressed_webhook_events:
                suppressed_webhook_events_item = suppressed_webhook_events_item_data.to_dict()
                suppressed_webhook_events.append(suppressed_webhook_events_item)

        incomplete_captured_events = self.incomplete_captured_events

        queue_capture_version: int | Unset = UNSET
        if not isinstance(self.queue_capture_version, Unset):
            queue_capture_version = self.queue_capture_version

        queue_messages: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.queue_messages, Unset):
            queue_messages = []
            for queue_messages_item_data in self.queue_messages:
                queue_messages_item = queue_messages_item_data.to_dict()
                queue_messages.append(queue_messages_item)

        incomplete_queue_messages = self.incomplete_queue_messages

        platform_capture_version: int | Unset = UNSET
        if not isinstance(self.platform_capture_version, Unset):
            platform_capture_version = self.platform_capture_version

        platform_calls: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.platform_calls, Unset):
            platform_calls = []
            for platform_calls_item_data in self.platform_calls:
                platform_calls_item = platform_calls_item_data.to_dict()
                platform_calls.append(platform_calls_item)

        incomplete_platform_calls = self.incomplete_platform_calls

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "scope": scope,
                "run_id": run_id,
                "status": status,
                "workloads": workloads,
                "intents": intents,
                "captured_count": captured_count,
                "pending_managed_work": pending_managed_work,
                "incomplete_managed_effects": incomplete_managed_effects,
                "suppressed_deliveries": suppressed_deliveries,
                "truncated": truncated,
            }
        )
        if suspend_resume is not UNSET:
            field_dict["suspend_resume"] = suspend_resume
        if cold_start is not UNSET:
            field_dict["cold_start"] = cold_start
        if managed_work is not UNSET:
            field_dict["managed_work"] = managed_work
        if recorder_version is not UNSET:
            field_dict["recorder_version"] = recorder_version
        if recorder_sessions is not UNSET:
            field_dict["recorder_sessions"] = recorder_sessions
        if pending_recorder_sessions is not UNSET:
            field_dict["pending_recorder_sessions"] = pending_recorder_sessions
        if incomplete_recorder_sessions is not UNSET:
            field_dict["incomplete_recorder_sessions"] = incomplete_recorder_sessions
        if sealed_at is not UNSET:
            field_dict["sealed_at"] = sealed_at
        if simulation_version is not UNSET:
            field_dict["simulation_version"] = simulation_version
        if simulation_sha256 is not UNSET:
            field_dict["simulation_sha256"] = simulation_sha256
        if simulated_attempts is not UNSET:
            field_dict["simulated_attempts"] = simulated_attempts
        if pending_simulation_attempts is not UNSET:
            field_dict["pending_simulation_attempts"] = pending_simulation_attempts
        if incomplete_simulation_attempts is not UNSET:
            field_dict["incomplete_simulation_attempts"] = incomplete_simulation_attempts
        if reason is not UNSET:
            field_dict["reason"] = reason
        if event_capture_version is not UNSET:
            field_dict["event_capture_version"] = event_capture_version
        if published_events is not UNSET:
            field_dict["published_events"] = published_events
        if suppressed_webhook_events is not UNSET:
            field_dict["suppressed_webhook_events"] = suppressed_webhook_events
        if incomplete_captured_events is not UNSET:
            field_dict["incomplete_captured_events"] = incomplete_captured_events
        if queue_capture_version is not UNSET:
            field_dict["queue_capture_version"] = queue_capture_version
        if queue_messages is not UNSET:
            field_dict["queue_messages"] = queue_messages
        if incomplete_queue_messages is not UNSET:
            field_dict["incomplete_queue_messages"] = incomplete_queue_messages
        if platform_capture_version is not UNSET:
            field_dict["platform_capture_version"] = platform_capture_version
        if platform_calls is not UNSET:
            field_dict["platform_calls"] = platform_calls
        if incomplete_platform_calls is not UNSET:
            field_dict["incomplete_platform_calls"] = incomplete_platform_calls

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_captured_action import ScenarioCapturedAction
        from ..models.scenario_captured_event import ScenarioCapturedEvent
        from ..models.scenario_captured_platform_call import ScenarioCapturedPlatformCall
        from ..models.scenario_captured_queue_message import ScenarioCapturedQueueMessage
        from ..models.scenario_cold_start_evidence import ScenarioColdStartEvidence
        from ..models.scenario_managed_work_evidence import ScenarioManagedWorkEvidence
        from ..models.scenario_simulation_attempt import ScenarioSimulationAttempt
        from ..models.scenario_suppressed_webhook_events import ScenarioSuppressedWebhookEvents
        from ..models.scenario_suspend_resume_evidence import ScenarioSuspendResumeEvidence

        d = dict(src_dict)
        version = check_scenario_action_snapshot_version(d.pop("version"))

        scope = check_scenario_action_snapshot_scope(d.pop("scope"))

        run_id = d.pop("run_id")

        status = check_scenario_action_snapshot_status(d.pop("status"))

        workloads = cast(list[str], d.pop("workloads"))

        intents = []
        _intents = d.pop("intents")
        for intents_item_data in _intents:
            intents_item = ScenarioCapturedAction.from_dict(intents_item_data)

            intents.append(intents_item)

        captured_count = d.pop("captured_count")

        pending_managed_work = d.pop("pending_managed_work")

        incomplete_managed_effects = d.pop("incomplete_managed_effects")

        suppressed_deliveries = d.pop("suppressed_deliveries")

        truncated = d.pop("truncated")

        _suspend_resume = d.pop("suspend_resume", UNSET)
        suspend_resume: ScenarioSuspendResumeEvidence | Unset
        if isinstance(_suspend_resume, Unset):
            suspend_resume = UNSET
        else:
            suspend_resume = ScenarioSuspendResumeEvidence.from_dict(_suspend_resume)

        _cold_start = d.pop("cold_start", UNSET)
        cold_start: ScenarioColdStartEvidence | Unset
        if isinstance(_cold_start, Unset):
            cold_start = UNSET
        else:
            cold_start = ScenarioColdStartEvidence.from_dict(_cold_start)

        _managed_work = d.pop("managed_work", UNSET)
        managed_work: ScenarioManagedWorkEvidence | Unset
        if isinstance(_managed_work, Unset):
            managed_work = UNSET
        else:
            managed_work = ScenarioManagedWorkEvidence.from_dict(_managed_work)

        _recorder_version = d.pop("recorder_version", UNSET)
        recorder_version: ScenarioActionSnapshotRecorderVersion | Unset
        if isinstance(_recorder_version, Unset):
            recorder_version = UNSET
        else:
            recorder_version = check_scenario_action_snapshot_recorder_version(_recorder_version)

        recorder_sessions = d.pop("recorder_sessions", UNSET)

        pending_recorder_sessions = d.pop("pending_recorder_sessions", UNSET)

        incomplete_recorder_sessions = d.pop("incomplete_recorder_sessions", UNSET)

        _sealed_at = d.pop("sealed_at", UNSET)
        sealed_at: datetime.datetime | Unset
        if isinstance(_sealed_at, Unset):
            sealed_at = UNSET
        else:
            sealed_at = datetime.datetime.fromisoformat(_sealed_at)

        _simulation_version = d.pop("simulation_version", UNSET)
        simulation_version: ScenarioActionSnapshotSimulationVersion | Unset
        if isinstance(_simulation_version, Unset):
            simulation_version = UNSET
        else:
            simulation_version = check_scenario_action_snapshot_simulation_version(_simulation_version)

        simulation_sha256 = d.pop("simulation_sha256", UNSET)

        _simulated_attempts = d.pop("simulated_attempts", UNSET)
        simulated_attempts: list[ScenarioSimulationAttempt] | Unset = UNSET
        if _simulated_attempts is not UNSET:
            simulated_attempts = []
            for simulated_attempts_item_data in _simulated_attempts:
                simulated_attempts_item = ScenarioSimulationAttempt.from_dict(simulated_attempts_item_data)

                simulated_attempts.append(simulated_attempts_item)

        pending_simulation_attempts = d.pop("pending_simulation_attempts", UNSET)

        incomplete_simulation_attempts = d.pop("incomplete_simulation_attempts", UNSET)

        reason = d.pop("reason", UNSET)

        _event_capture_version = d.pop("event_capture_version", UNSET)
        event_capture_version: ScenarioActionSnapshotEventCaptureVersion | Unset
        if isinstance(_event_capture_version, Unset):
            event_capture_version = UNSET
        else:
            event_capture_version = check_scenario_action_snapshot_event_capture_version(_event_capture_version)

        _published_events = d.pop("published_events", UNSET)
        published_events: list[ScenarioCapturedEvent] | Unset = UNSET
        if _published_events is not UNSET:
            published_events = []
            for published_events_item_data in _published_events:
                published_events_item = ScenarioCapturedEvent.from_dict(published_events_item_data)

                published_events.append(published_events_item)

        _suppressed_webhook_events = d.pop("suppressed_webhook_events", UNSET)
        suppressed_webhook_events: list[ScenarioSuppressedWebhookEvents] | Unset = UNSET
        if _suppressed_webhook_events is not UNSET:
            suppressed_webhook_events = []
            for suppressed_webhook_events_item_data in _suppressed_webhook_events:
                suppressed_webhook_events_item = ScenarioSuppressedWebhookEvents.from_dict(
                    suppressed_webhook_events_item_data
                )

                suppressed_webhook_events.append(suppressed_webhook_events_item)

        incomplete_captured_events = d.pop("incomplete_captured_events", UNSET)

        _queue_capture_version = d.pop("queue_capture_version", UNSET)
        queue_capture_version: ScenarioActionSnapshotQueueCaptureVersion | Unset
        if isinstance(_queue_capture_version, Unset):
            queue_capture_version = UNSET
        else:
            queue_capture_version = check_scenario_action_snapshot_queue_capture_version(_queue_capture_version)

        _queue_messages = d.pop("queue_messages", UNSET)
        queue_messages: list[ScenarioCapturedQueueMessage] | Unset = UNSET
        if _queue_messages is not UNSET:
            queue_messages = []
            for queue_messages_item_data in _queue_messages:
                queue_messages_item = ScenarioCapturedQueueMessage.from_dict(queue_messages_item_data)

                queue_messages.append(queue_messages_item)

        incomplete_queue_messages = d.pop("incomplete_queue_messages", UNSET)

        _platform_capture_version = d.pop("platform_capture_version", UNSET)
        platform_capture_version: ScenarioActionSnapshotPlatformCaptureVersion | Unset
        if isinstance(_platform_capture_version, Unset):
            platform_capture_version = UNSET
        else:
            platform_capture_version = check_scenario_action_snapshot_platform_capture_version(
                _platform_capture_version
            )

        _platform_calls = d.pop("platform_calls", UNSET)
        platform_calls: list[ScenarioCapturedPlatformCall] | Unset = UNSET
        if _platform_calls is not UNSET:
            platform_calls = []
            for platform_calls_item_data in _platform_calls:
                platform_calls_item = ScenarioCapturedPlatformCall.from_dict(platform_calls_item_data)

                platform_calls.append(platform_calls_item)

        incomplete_platform_calls = d.pop("incomplete_platform_calls", UNSET)

        scenario_action_snapshot = cls(
            version=version,
            scope=scope,
            run_id=run_id,
            status=status,
            workloads=workloads,
            intents=intents,
            captured_count=captured_count,
            pending_managed_work=pending_managed_work,
            incomplete_managed_effects=incomplete_managed_effects,
            suppressed_deliveries=suppressed_deliveries,
            truncated=truncated,
            suspend_resume=suspend_resume,
            cold_start=cold_start,
            managed_work=managed_work,
            recorder_version=recorder_version,
            recorder_sessions=recorder_sessions,
            pending_recorder_sessions=pending_recorder_sessions,
            incomplete_recorder_sessions=incomplete_recorder_sessions,
            sealed_at=sealed_at,
            simulation_version=simulation_version,
            simulation_sha256=simulation_sha256,
            simulated_attempts=simulated_attempts,
            pending_simulation_attempts=pending_simulation_attempts,
            incomplete_simulation_attempts=incomplete_simulation_attempts,
            reason=reason,
            event_capture_version=event_capture_version,
            published_events=published_events,
            suppressed_webhook_events=suppressed_webhook_events,
            incomplete_captured_events=incomplete_captured_events,
            queue_capture_version=queue_capture_version,
            queue_messages=queue_messages,
            incomplete_queue_messages=incomplete_queue_messages,
            platform_capture_version=platform_capture_version,
            platform_calls=platform_calls,
            incomplete_platform_calls=incomplete_platform_calls,
        )

        scenario_action_snapshot.additional_properties = d
        return scenario_action_snapshot

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
