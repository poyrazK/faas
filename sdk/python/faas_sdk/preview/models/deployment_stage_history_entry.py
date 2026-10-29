from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_stage_history_entry_name import (
    DeploymentStageHistoryEntryName,
    check_deployment_stage_history_entry_name,
)
from ..models.deployment_stage_history_entry_status import (
    DeploymentStageHistoryEntryStatus,
    check_deployment_stage_history_entry_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentStageHistoryEntry")


@_attrs_define
class DeploymentStageHistoryEntry:
    """One closed stage with server-measured duration."""

    name: DeploymentStageHistoryEntryName
    started_at: datetime.datetime | None
    ended_at: datetime.datetime | None
    duration_ms: int
    status: DeploymentStageHistoryEntryStatus
    reason: str | Unset = UNSET
    """Failure or cancellation detail; absent or empty for completed stages."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name: str = self.name

        started_at: None | str
        if isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        ended_at: None | str
        if isinstance(self.ended_at, datetime.datetime):
            ended_at = self.ended_at.isoformat()
        else:
            ended_at = self.ended_at

        duration_ms = self.duration_ms

        status: str = self.status

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "started_at": started_at,
                "ended_at": ended_at,
                "duration_ms": duration_ms,
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = check_deployment_stage_history_entry_name(d.pop("name"))

        def _parse_started_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        started_at = _parse_started_at(d.pop("started_at"))

        def _parse_ended_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                ended_at_type_0 = datetime.datetime.fromisoformat(data)

                return ended_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        ended_at = _parse_ended_at(d.pop("ended_at"))

        duration_ms = d.pop("duration_ms")

        status = check_deployment_stage_history_entry_status(d.pop("status"))

        reason = d.pop("reason", UNSET)

        deployment_stage_history_entry = cls(
            name=name,
            started_at=started_at,
            ended_at=ended_at,
            duration_ms=duration_ms,
            status=status,
            reason=reason,
        )

        deployment_stage_history_entry.additional_properties = d
        return deployment_stage_history_entry

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
