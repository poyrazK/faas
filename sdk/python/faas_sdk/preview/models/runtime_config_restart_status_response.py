from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.runtime_config_restart_status_response_failure_reason import (
    RuntimeConfigRestartStatusResponseFailureReason,
    check_runtime_config_restart_status_response_failure_reason,
)
from ..models.runtime_config_restart_status_response_status import (
    RuntimeConfigRestartStatusResponseStatus,
    check_runtime_config_restart_status_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RuntimeConfigRestartStatusResponse")


@_attrs_define
class RuntimeConfigRestartStatusResponse:
    """Durable status for an accepted fresh runtime-configuration restart."""

    wake_id: UUID
    status: RuntimeConfigRestartStatusResponseStatus
    attempts: int
    """Number of scheduler handling attempts so far."""
    requested_at: datetime.datetime
    failure_reason: RuntimeConfigRestartStatusResponseFailureReason | Unset = UNSET
    """Stable safe reason from the most recent unsuccessful attempt, when available."""
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_id = str(self.wake_id)

        status: str = self.status

        attempts = self.attempts

        requested_at = self.requested_at.isoformat()

        failure_reason: str | Unset = UNSET
        if not isinstance(self.failure_reason, Unset):
            failure_reason = self.failure_reason

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "wake_id": wake_id,
                "status": status,
                "attempts": attempts,
                "requested_at": requested_at,
            }
        )
        if failure_reason is not UNSET:
            field_dict["failure_reason"] = failure_reason
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wake_id = UUID(d.pop("wake_id"))

        status = check_runtime_config_restart_status_response_status(d.pop("status"))

        attempts = d.pop("attempts")

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        _failure_reason = d.pop("failure_reason", UNSET)
        failure_reason: RuntimeConfigRestartStatusResponseFailureReason | Unset
        if isinstance(_failure_reason, Unset):
            failure_reason = UNSET
        else:
            failure_reason = check_runtime_config_restart_status_response_failure_reason(_failure_reason)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        runtime_config_restart_status_response = cls(
            wake_id=wake_id,
            status=status,
            attempts=attempts,
            requested_at=requested_at,
            failure_reason=failure_reason,
            completed_at=completed_at,
        )

        runtime_config_restart_status_response.additional_properties = d
        return runtime_config_restart_status_response

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
