from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_repair_health_item_node_status import (
    DataRepairHealthItemNodeStatus,
    check_data_repair_health_item_node_status,
)
from ..models.data_repair_health_item_phase import DataRepairHealthItemPhase, check_data_repair_health_item_phase
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataRepairHealthItem")


@_attrs_define
class DataRepairHealthItem:
    """Operator recovery metadata for an original rehearsal task, apply operation or pending replacement."""

    cursor_key: str
    operation_id: UUID
    account_id: UUID
    app_id: UUID
    phase: DataRepairHealthItemPhase
    phase_started_at: datetime.datetime
    age_seconds: int
    node_status: DataRepairHealthItemNodeStatus
    capacity_held: bool
    held_memory_mb: int
    held_vcpus: int
    cancel_requested: bool
    needs_attention: bool
    recovery_guidance: str
    runbook_url: str
    task_id: UUID | Unset = UNSET
    node_id: UUID | Unset = UNSET
    failure_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cursor_key = self.cursor_key

        operation_id = str(self.operation_id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        phase: str = self.phase

        phase_started_at = self.phase_started_at.isoformat()

        age_seconds = self.age_seconds

        node_status: str = self.node_status

        capacity_held = self.capacity_held

        held_memory_mb = self.held_memory_mb

        held_vcpus = self.held_vcpus

        cancel_requested = self.cancel_requested

        needs_attention = self.needs_attention

        recovery_guidance = self.recovery_guidance

        runbook_url = self.runbook_url

        task_id: str | Unset = UNSET
        if not isinstance(self.task_id, Unset):
            task_id = str(self.task_id)

        node_id: str | Unset = UNSET
        if not isinstance(self.node_id, Unset):
            node_id = str(self.node_id)

        failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cursor_key": cursor_key,
                "operation_id": operation_id,
                "account_id": account_id,
                "app_id": app_id,
                "phase": phase,
                "phase_started_at": phase_started_at,
                "age_seconds": age_seconds,
                "node_status": node_status,
                "capacity_held": capacity_held,
                "held_memory_mb": held_memory_mb,
                "held_vcpus": held_vcpus,
                "cancel_requested": cancel_requested,
                "needs_attention": needs_attention,
                "recovery_guidance": recovery_guidance,
                "runbook_url": runbook_url,
            }
        )
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if node_id is not UNSET:
            field_dict["node_id"] = node_id
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cursor_key = d.pop("cursor_key")

        operation_id = UUID(d.pop("operation_id"))

        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        phase = check_data_repair_health_item_phase(d.pop("phase"))

        phase_started_at = datetime.datetime.fromisoformat(d.pop("phase_started_at"))

        age_seconds = d.pop("age_seconds")

        node_status = check_data_repair_health_item_node_status(d.pop("node_status"))

        capacity_held = d.pop("capacity_held")

        held_memory_mb = d.pop("held_memory_mb")

        held_vcpus = d.pop("held_vcpus")

        cancel_requested = d.pop("cancel_requested")

        needs_attention = d.pop("needs_attention")

        recovery_guidance = d.pop("recovery_guidance")

        runbook_url = d.pop("runbook_url")

        _task_id = d.pop("task_id", UNSET)
        task_id: UUID | Unset
        if isinstance(_task_id, Unset):
            task_id = UNSET
        else:
            task_id = UUID(_task_id)

        _node_id = d.pop("node_id", UNSET)
        node_id: UUID | Unset
        if isinstance(_node_id, Unset):
            node_id = UNSET
        else:
            node_id = UUID(_node_id)

        failure_code = d.pop("failure_code", UNSET)

        data_repair_health_item = cls(
            cursor_key=cursor_key,
            operation_id=operation_id,
            account_id=account_id,
            app_id=app_id,
            phase=phase,
            phase_started_at=phase_started_at,
            age_seconds=age_seconds,
            node_status=node_status,
            capacity_held=capacity_held,
            held_memory_mb=held_memory_mb,
            held_vcpus=held_vcpus,
            cancel_requested=cancel_requested,
            needs_attention=needs_attention,
            recovery_guidance=recovery_guidance,
            runbook_url=runbook_url,
            task_id=task_id,
            node_id=node_id,
            failure_code=failure_code,
        )

        data_repair_health_item.additional_properties = d
        return data_repair_health_item

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
