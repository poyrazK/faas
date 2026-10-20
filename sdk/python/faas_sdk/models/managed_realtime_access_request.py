from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_realtime_access_request_action import (
    ManagedRealtimeAccessRequestAction,
    check_managed_realtime_access_request_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeAccessRequest")


@_attrs_define
class ManagedRealtimeAccessRequest:
    """Managed Realtime Access Request."""

    action: ManagedRealtimeAccessRequestAction
    channel: str | Unset = UNSET
    """Required for channel actions; omitted for revoke_sessions."""

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        channel = self.channel

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action": action,
            }
        )
        if channel is not UNSET:
            field_dict["channel"] = channel

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_managed_realtime_access_request_action(d.pop("action"))

        channel = d.pop("channel", UNSET)

        managed_realtime_access_request = cls(
            action=action,
            channel=channel,
        )

        return managed_realtime_access_request
