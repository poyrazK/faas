from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_refresh_failure_reason import BindingRefreshFailureReason, check_binding_refresh_failure_reason
from ..models.binding_refresh_status import BindingRefreshStatus, check_binding_refresh_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingRefresh")


@_attrs_define
class BindingRefresh:
    """Durable rolling restart handoff for a pending PostgreSQL or object-storage rotation. Completion does not imply fresh
    resident instances or successful verification. Not_queued means no retained outbox record was found; unknown means
    progress could not be read.

    """

    wake_id: UUID
    status: BindingRefreshStatus
    attempts: int
    failure_reason: BindingRefreshFailureReason | Unset = UNSET
    requested_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_id = str(self.wake_id)

        status: str = self.status

        attempts = self.attempts

        failure_reason: str | Unset = UNSET
        if not isinstance(self.failure_reason, Unset):
            failure_reason = self.failure_reason

        requested_at: str | Unset = UNSET
        if not isinstance(self.requested_at, Unset):
            requested_at = self.requested_at.isoformat()

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
            }
        )
        if failure_reason is not UNSET:
            field_dict["failure_reason"] = failure_reason
        if requested_at is not UNSET:
            field_dict["requested_at"] = requested_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wake_id = UUID(d.pop("wake_id"))

        status = check_binding_refresh_status(d.pop("status"))

        attempts = d.pop("attempts")

        _failure_reason = d.pop("failure_reason", UNSET)
        failure_reason: BindingRefreshFailureReason | Unset
        if isinstance(_failure_reason, Unset):
            failure_reason = UNSET
        else:
            failure_reason = check_binding_refresh_failure_reason(_failure_reason)

        _requested_at = d.pop("requested_at", UNSET)
        requested_at: datetime.datetime | Unset
        if isinstance(_requested_at, Unset):
            requested_at = UNSET
        else:
            requested_at = datetime.datetime.fromisoformat(_requested_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        binding_refresh = cls(
            wake_id=wake_id,
            status=status,
            attempts=attempts,
            failure_reason=failure_reason,
            requested_at=requested_at,
            completed_at=completed_at,
        )

        binding_refresh.additional_properties = d
        return binding_refresh

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
