from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_automatic_queue_entry import WorkflowRecoveryAutomaticQueueEntry
    from ..models.workflow_recovery_health_incident import WorkflowRecoveryHealthIncident
    from ..models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse


T = TypeVar("T", bound="WorkflowRecoveryAutomaticQueue")


@_attrs_define
class WorkflowRecoveryAutomaticQueue:
    """Informational automatic retry queue snapshot. Outstanding means a child has been created but final proof has not
    been recorded; it does not assert the child is currently running. Pending counts current policy intents only.
    Charges survive review pruning and policy replacement. Paused policies retain their queue and committed children
    continue. Missing evidence is unresolved, never success or failure.

    """

    policy: WorkflowRecoveryPolicyResponse
    """Current recovery policy with trusted update actor and scheduler status. A versioned policy replacement
    resets its automatic failure pause and suppresses older pending automatic actions."""
    observed_at: datetime.datetime
    pending: int
    outstanding: int
    hourly_executions: int
    remaining_budget: int
    entries: list[WorkflowRecoveryAutomaticQueueEntry]
    truncated: bool
    health_incident: WorkflowRecoveryHealthIncident | Unset = UNSET
    """Latest retained health incident per app across policy versions. Reason changes do not create repeated
    alerts. Unknown snapshots defer initial capture and cannot declare recovery. Disabling alerts closes silently;
    healthy or inactive observations close with recovery only after alert capture. Historical webhook payloads
    remain immutable."""
    cooldown_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy.to_dict()

        observed_at = self.observed_at.isoformat()

        pending = self.pending

        outstanding = self.outstanding

        hourly_executions = self.hourly_executions

        remaining_budget = self.remaining_budget

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        truncated = self.truncated

        health_incident: dict[str, Any] | Unset = UNSET
        if not isinstance(self.health_incident, Unset):
            health_incident = self.health_incident.to_dict()

        cooldown_until: str | Unset = UNSET
        if not isinstance(self.cooldown_until, Unset):
            cooldown_until = self.cooldown_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy": policy,
                "observed_at": observed_at,
                "pending": pending,
                "outstanding": outstanding,
                "hourly_executions": hourly_executions,
                "remaining_budget": remaining_budget,
                "entries": entries,
                "truncated": truncated,
            }
        )
        if health_incident is not UNSET:
            field_dict["health_incident"] = health_incident
        if cooldown_until is not UNSET:
            field_dict["cooldown_until"] = cooldown_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_automatic_queue_entry import WorkflowRecoveryAutomaticQueueEntry
        from ..models.workflow_recovery_health_incident import WorkflowRecoveryHealthIncident
        from ..models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse

        d = dict(src_dict)
        policy = WorkflowRecoveryPolicyResponse.from_dict(d.pop("policy"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        pending = d.pop("pending")

        outstanding = d.pop("outstanding")

        hourly_executions = d.pop("hourly_executions")

        remaining_budget = d.pop("remaining_budget")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = WorkflowRecoveryAutomaticQueueEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        truncated = d.pop("truncated")

        _health_incident = d.pop("health_incident", UNSET)
        health_incident: WorkflowRecoveryHealthIncident | Unset
        if isinstance(_health_incident, Unset):
            health_incident = UNSET
        else:
            health_incident = WorkflowRecoveryHealthIncident.from_dict(_health_incident)

        _cooldown_until = d.pop("cooldown_until", UNSET)
        cooldown_until: datetime.datetime | Unset
        if isinstance(_cooldown_until, Unset):
            cooldown_until = UNSET
        else:
            cooldown_until = datetime.datetime.fromisoformat(_cooldown_until)

        workflow_recovery_automatic_queue = cls(
            policy=policy,
            observed_at=observed_at,
            pending=pending,
            outstanding=outstanding,
            hourly_executions=hourly_executions,
            remaining_budget=remaining_budget,
            entries=entries,
            truncated=truncated,
            health_incident=health_incident,
            cooldown_until=cooldown_until,
        )

        workflow_recovery_automatic_queue.additional_properties = d
        return workflow_recovery_automatic_queue

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
