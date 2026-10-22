from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_timeline_phase import ReleaseTimelinePhase, check_release_timeline_phase
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_approval_status import ReleaseApprovalStatus
    from ..models.release_gate_result import ReleaseGateResult
    from ..models.release_timeline_event import ReleaseTimelineEvent
    from ..models.scheduled_release import ScheduledRelease


T = TypeVar("T", bound="ReleaseTimeline")


@_attrs_define
class ReleaseTimeline:
    """One release's phase and what happened to it (ADR-20261022120744)."""

    deployment_id: UUID
    app: str
    status: str
    """The deployment's status, such as live or superseded."""
    phase: ReleaseTimelinePhase
    """Where the release is, matched in this order."""
    summary: str
    """One line, such as "Rolling out: step 2 of 4, 10% of traffic"."""
    traffic_percent: int
    created_at: datetime.datetime
    events: list[ReleaseTimelineEvent]
    revision: int | Unset = UNSET
    scope: str | Unset = UNSET
    canary_step: int | Unset = UNSET
    """Zero-based current canary step."""
    canary_total_steps: int | Unset = UNSET
    deployed_by: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    pr_number: int | Unset = UNSET
    tag: str | Unset = UNSET
    reason: str | Unset = UNSET
    stable_deployment_id: UUID | Unset = UNSET
    """The release this one replaces; an abort returns traffic to it."""
    stable_revision: int | Unset = UNSET
    replaced_by_deployment_id: UUID | Unset = UNSET
    """The newer release of the same scope that took over."""
    replaced_by_revision: int | Unset = UNSET
    watch_until: datetime.datetime | Unset = UNSET
    """When the post-release watch stops judging the release."""
    approval: ReleaseApprovalStatus | Unset = UNSET
    """A release's approval state (ADR-20261020085706)."""
    schedule: ScheduledRelease | Unset = UNSET
    """A release built ahead of time that starts its rollout at release_at (ADR-20261021152201)."""
    gates: list[ReleaseGateResult] | Unset = UNSET
    """The results of the newest decision that judged release gates."""
    truncated: bool | Unset = UNSET
    """Older audit history was left out."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        app = self.app

        status = self.status

        phase: str = self.phase

        summary = self.summary

        traffic_percent = self.traffic_percent

        created_at = self.created_at.isoformat()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        revision = self.revision

        scope = self.scope

        canary_step = self.canary_step

        canary_total_steps = self.canary_total_steps

        deployed_by = self.deployed_by

        commit_sha = self.commit_sha

        pr_number = self.pr_number

        tag = self.tag

        reason = self.reason

        stable_deployment_id: str | Unset = UNSET
        if not isinstance(self.stable_deployment_id, Unset):
            stable_deployment_id = str(self.stable_deployment_id)

        stable_revision = self.stable_revision

        replaced_by_deployment_id: str | Unset = UNSET
        if not isinstance(self.replaced_by_deployment_id, Unset):
            replaced_by_deployment_id = str(self.replaced_by_deployment_id)

        replaced_by_revision = self.replaced_by_revision

        watch_until: str | Unset = UNSET
        if not isinstance(self.watch_until, Unset):
            watch_until = self.watch_until.isoformat()

        approval: dict[str, Any] | Unset = UNSET
        if not isinstance(self.approval, Unset):
            approval = self.approval.to_dict()

        schedule: dict[str, Any] | Unset = UNSET
        if not isinstance(self.schedule, Unset):
            schedule = self.schedule.to_dict()

        gates: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.gates, Unset):
            gates = []
            for gates_item_data in self.gates:
                gates_item = gates_item_data.to_dict()
                gates.append(gates_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "app": app,
                "status": status,
                "phase": phase,
                "summary": summary,
                "traffic_percent": traffic_percent,
                "created_at": created_at,
                "events": events,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision
        if scope is not UNSET:
            field_dict["scope"] = scope
        if canary_step is not UNSET:
            field_dict["canary_step"] = canary_step
        if canary_total_steps is not UNSET:
            field_dict["canary_total_steps"] = canary_total_steps
        if deployed_by is not UNSET:
            field_dict["deployed_by"] = deployed_by
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if pr_number is not UNSET:
            field_dict["pr_number"] = pr_number
        if tag is not UNSET:
            field_dict["tag"] = tag
        if reason is not UNSET:
            field_dict["reason"] = reason
        if stable_deployment_id is not UNSET:
            field_dict["stable_deployment_id"] = stable_deployment_id
        if stable_revision is not UNSET:
            field_dict["stable_revision"] = stable_revision
        if replaced_by_deployment_id is not UNSET:
            field_dict["replaced_by_deployment_id"] = replaced_by_deployment_id
        if replaced_by_revision is not UNSET:
            field_dict["replaced_by_revision"] = replaced_by_revision
        if watch_until is not UNSET:
            field_dict["watch_until"] = watch_until
        if approval is not UNSET:
            field_dict["approval"] = approval
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if gates is not UNSET:
            field_dict["gates"] = gates
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_approval_status import ReleaseApprovalStatus
        from ..models.release_gate_result import ReleaseGateResult
        from ..models.release_timeline_event import ReleaseTimelineEvent
        from ..models.scheduled_release import ScheduledRelease

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        app = d.pop("app")

        status = d.pop("status")

        phase = check_release_timeline_phase(d.pop("phase"))

        summary = d.pop("summary")

        traffic_percent = d.pop("traffic_percent")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = ReleaseTimelineEvent.from_dict(events_item_data)

            events.append(events_item)

        revision = d.pop("revision", UNSET)

        scope = d.pop("scope", UNSET)

        canary_step = d.pop("canary_step", UNSET)

        canary_total_steps = d.pop("canary_total_steps", UNSET)

        deployed_by = d.pop("deployed_by", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        pr_number = d.pop("pr_number", UNSET)

        tag = d.pop("tag", UNSET)

        reason = d.pop("reason", UNSET)

        _stable_deployment_id = d.pop("stable_deployment_id", UNSET)
        stable_deployment_id: UUID | Unset
        if isinstance(_stable_deployment_id, Unset):
            stable_deployment_id = UNSET
        else:
            stable_deployment_id = UUID(_stable_deployment_id)

        stable_revision = d.pop("stable_revision", UNSET)

        _replaced_by_deployment_id = d.pop("replaced_by_deployment_id", UNSET)
        replaced_by_deployment_id: UUID | Unset
        if isinstance(_replaced_by_deployment_id, Unset):
            replaced_by_deployment_id = UNSET
        else:
            replaced_by_deployment_id = UUID(_replaced_by_deployment_id)

        replaced_by_revision = d.pop("replaced_by_revision", UNSET)

        _watch_until = d.pop("watch_until", UNSET)
        watch_until: datetime.datetime | Unset
        if isinstance(_watch_until, Unset):
            watch_until = UNSET
        else:
            watch_until = datetime.datetime.fromisoformat(_watch_until)

        _approval = d.pop("approval", UNSET)
        approval: ReleaseApprovalStatus | Unset
        if isinstance(_approval, Unset):
            approval = UNSET
        else:
            approval = ReleaseApprovalStatus.from_dict(_approval)

        _schedule = d.pop("schedule", UNSET)
        schedule: ScheduledRelease | Unset
        if isinstance(_schedule, Unset):
            schedule = UNSET
        else:
            schedule = ScheduledRelease.from_dict(_schedule)

        _gates = d.pop("gates", UNSET)
        gates: list[ReleaseGateResult] | Unset = UNSET
        if _gates is not UNSET:
            gates = []
            for gates_item_data in _gates:
                gates_item = ReleaseGateResult.from_dict(gates_item_data)

                gates.append(gates_item)

        truncated = d.pop("truncated", UNSET)

        release_timeline = cls(
            deployment_id=deployment_id,
            app=app,
            status=status,
            phase=phase,
            summary=summary,
            traffic_percent=traffic_percent,
            created_at=created_at,
            events=events,
            revision=revision,
            scope=scope,
            canary_step=canary_step,
            canary_total_steps=canary_total_steps,
            deployed_by=deployed_by,
            commit_sha=commit_sha,
            pr_number=pr_number,
            tag=tag,
            reason=reason,
            stable_deployment_id=stable_deployment_id,
            stable_revision=stable_revision,
            replaced_by_deployment_id=replaced_by_deployment_id,
            replaced_by_revision=replaced_by_revision,
            watch_until=watch_until,
            approval=approval,
            schedule=schedule,
            gates=gates,
            truncated=truncated,
        )

        release_timeline.additional_properties = d
        return release_timeline

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
