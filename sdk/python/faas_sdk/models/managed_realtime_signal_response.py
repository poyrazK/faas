from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_signal_response_member_id import (
    ManagedRealtimeSignalResponseMemberId,
    check_managed_realtime_signal_response_member_id,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeSignalResponse")


@_attrs_define
class ManagedRealtimeSignalResponse:
    """Best-effort signal fanout result for currently connected subscribers."""

    accepted: bool
    member_id: ManagedRealtimeSignalResponseMemberId
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        accepted = self.accepted

        member_id: str = self.member_id

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "accepted": accepted,
                "member_id": member_id,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        accepted = d.pop("accepted")

        member_id = check_managed_realtime_signal_response_member_id(d.pop("member_id"))

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        managed_realtime_signal_response = cls(
            accepted=accepted,
            member_id=member_id,
            expires_at=expires_at,
        )

        managed_realtime_signal_response.additional_properties = d
        return managed_realtime_signal_response

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
