from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppPublishEventRequest")


@_attrs_define
class AppPublishEventRequest:
    """Producer-key event publication with a 1 MiB total body limit."""

    key: str
    """Stable exact application-scoped producer key; preserve on retry."""
    type_: str
    data: Any
    """Event data associated with this producer key."""
    expected_app_id: UUID | Unset = UNSET
    """Optional canonical application UUID guard; mismatches return 409 before publication or comparison."""
    time: datetime.datetime | Unset = UNSET
    """Original occurrence time; defaults to ingress time."""
    schemaversion: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        type_ = self.type_

        data = self.data

        expected_app_id: str | Unset = UNSET
        if not isinstance(self.expected_app_id, Unset):
            expected_app_id = str(self.expected_app_id)

        time: str | Unset = UNSET
        if not isinstance(self.time, Unset):
            time = self.time.isoformat()

        schemaversion = self.schemaversion

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "type": type_,
                "data": data,
            }
        )
        if expected_app_id is not UNSET:
            field_dict["expected_app_id"] = expected_app_id
        if time is not UNSET:
            field_dict["time"] = time
        if schemaversion is not UNSET:
            field_dict["schemaversion"] = schemaversion

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        type_ = d.pop("type")

        data = d.pop("data")

        _expected_app_id = d.pop("expected_app_id", UNSET)
        expected_app_id: UUID | Unset
        if isinstance(_expected_app_id, Unset):
            expected_app_id = UNSET
        else:
            expected_app_id = UUID(_expected_app_id)

        _time = d.pop("time", UNSET)
        time: datetime.datetime | Unset
        if isinstance(_time, Unset):
            time = UNSET
        else:
            time = datetime.datetime.fromisoformat(_time)

        schemaversion = d.pop("schemaversion", UNSET)

        app_publish_event_request = cls(
            key=key,
            type_=type_,
            data=data,
            expected_app_id=expected_app_id,
            time=time,
            schemaversion=schemaversion,
        )

        return app_publish_event_request
