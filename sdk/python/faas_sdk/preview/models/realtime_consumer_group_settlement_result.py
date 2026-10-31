from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_settlement_result_status import (
    RealtimeConsumerGroupSettlementResultStatus,
    check_realtime_consumer_group_settlement_result_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeConsumerGroupSettlementResult")


@_attrs_define
class RealtimeConsumerGroupSettlementResult:
    """Realtime Consumer Group Settlement Result."""

    status: RealtimeConsumerGroupSettlementResultStatus
    lease_until: datetime.datetime | Unset = UNSET
    available_at: datetime.datetime | Unset = UNSET
    """Earliest eligibility timestamp returned only for ready retry results."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        available_at: str | Unset = UNSET
        if not isinstance(self.available_at, Unset):
            available_at = self.available_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until
        if available_at is not UNSET:
            field_dict["available_at"] = available_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_realtime_consumer_group_settlement_result_status(d.pop("status"))

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        _available_at = d.pop("available_at", UNSET)
        available_at: datetime.datetime | Unset
        if isinstance(_available_at, Unset):
            available_at = UNSET
        else:
            available_at = datetime.datetime.fromisoformat(_available_at)

        realtime_consumer_group_settlement_result = cls(
            status=status,
            lease_until=lease_until,
            available_at=available_at,
        )

        realtime_consumer_group_settlement_result.additional_properties = d
        return realtime_consumer_group_settlement_result

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
