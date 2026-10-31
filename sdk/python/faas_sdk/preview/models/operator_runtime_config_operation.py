from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_runtime_config_operation_apply_mode import (
    OperatorRuntimeConfigOperationApplyMode,
    check_operator_runtime_config_operation_apply_mode,
)
from ..models.operator_runtime_config_operation_scope import (
    OperatorRuntimeConfigOperationScope,
    check_operator_runtime_config_operation_scope,
)
from ..models.operator_runtime_config_operation_status import (
    OperatorRuntimeConfigOperationStatus,
    check_operator_runtime_config_operation_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorRuntimeConfigOperation")


@_attrs_define
class OperatorRuntimeConfigOperation:
    """Durable asynchronous runtime-configuration apply request."""

    id: UUID
    key: str
    scope: OperatorRuntimeConfigOperationScope
    scope_id: str
    version: int
    desired_value: Any
    effective_value: Any
    apply_mode: OperatorRuntimeConfigOperationApplyMode
    status: OperatorRuntimeConfigOperationStatus
    phase: str
    reason: str
    target_count: int
    applied_count: int
    failed_count: int
    requested_at: datetime.datetime
    error: str | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        key = self.key

        scope: str = self.scope

        scope_id = self.scope_id

        version = self.version

        desired_value = self.desired_value

        effective_value = self.effective_value

        apply_mode: str = self.apply_mode

        status: str = self.status

        phase = self.phase

        reason = self.reason

        target_count = self.target_count

        applied_count = self.applied_count

        failed_count = self.failed_count

        requested_at = self.requested_at.isoformat()

        error = self.error

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "key": key,
                "scope": scope,
                "scope_id": scope_id,
                "version": version,
                "desired_value": desired_value,
                "effective_value": effective_value,
                "apply_mode": apply_mode,
                "status": status,
                "phase": phase,
                "reason": reason,
                "target_count": target_count,
                "applied_count": applied_count,
                "failed_count": failed_count,
                "requested_at": requested_at,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        key = d.pop("key")

        scope = check_operator_runtime_config_operation_scope(d.pop("scope"))

        scope_id = d.pop("scope_id")

        version = d.pop("version")

        desired_value = d.pop("desired_value")

        effective_value = d.pop("effective_value")

        apply_mode = check_operator_runtime_config_operation_apply_mode(d.pop("apply_mode"))

        status = check_operator_runtime_config_operation_status(d.pop("status"))

        phase = d.pop("phase")

        reason = d.pop("reason")

        target_count = d.pop("target_count")

        applied_count = d.pop("applied_count")

        failed_count = d.pop("failed_count")

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        error = d.pop("error", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        operator_runtime_config_operation = cls(
            id=id,
            key=key,
            scope=scope,
            scope_id=scope_id,
            version=version,
            desired_value=desired_value,
            effective_value=effective_value,
            apply_mode=apply_mode,
            status=status,
            phase=phase,
            reason=reason,
            target_count=target_count,
            applied_count=applied_count,
            failed_count=failed_count,
            requested_at=requested_at,
            error=error,
            started_at=started_at,
            finished_at=finished_at,
        )

        operator_runtime_config_operation.additional_properties = d
        return operator_runtime_config_operation

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
