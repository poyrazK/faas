from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeAccessResponse")


@_attrs_define
class ManagedRealtimeAccessResponse:
    """Managed Realtime Access Response."""

    denied_channels: list[str]
    revision: int
    updated_at: datetime.datetime
    sessions_revoked_before: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        denied_channels = self.denied_channels

        revision = self.revision

        updated_at = self.updated_at.isoformat()

        sessions_revoked_before: str | Unset = UNSET
        if not isinstance(self.sessions_revoked_before, Unset):
            sessions_revoked_before = self.sessions_revoked_before.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "denied_channels": denied_channels,
                "revision": revision,
                "updated_at": updated_at,
            }
        )
        if sessions_revoked_before is not UNSET:
            field_dict["sessions_revoked_before"] = sessions_revoked_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        denied_channels = cast(list[str], d.pop("denied_channels"))

        revision = d.pop("revision")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _sessions_revoked_before = d.pop("sessions_revoked_before", UNSET)
        sessions_revoked_before: datetime.datetime | Unset
        if isinstance(_sessions_revoked_before, Unset):
            sessions_revoked_before = UNSET
        else:
            sessions_revoked_before = datetime.datetime.fromisoformat(_sessions_revoked_before)

        managed_realtime_access_response = cls(
            denied_channels=denied_channels,
            revision=revision,
            updated_at=updated_at,
            sessions_revoked_before=sessions_revoked_before,
        )

        return managed_realtime_access_response
