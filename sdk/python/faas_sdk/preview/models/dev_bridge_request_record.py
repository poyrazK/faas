from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DevBridgeRequestRecord")


@_attrs_define
class DevBridgeRequestRecord:
    """Redacted request metadata without queries, headers or bodies."""

    id: int
    started_at: datetime.datetime
    method: str
    path: str
    status: int
    duration_ms: int
    response_bytes: int
    complete: bool
    error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        started_at = self.started_at.isoformat()

        method = self.method

        path = self.path

        status = self.status

        duration_ms = self.duration_ms

        response_bytes = self.response_bytes

        complete = self.complete

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "started_at": started_at,
                "method": method,
                "path": path,
                "status": status,
                "duration_ms": duration_ms,
                "response_bytes": response_bytes,
                "complete": complete,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        method = d.pop("method")

        path = d.pop("path")

        status = d.pop("status")

        duration_ms = d.pop("duration_ms")

        response_bytes = d.pop("response_bytes")

        complete = d.pop("complete")

        error = d.pop("error", UNSET)

        dev_bridge_request_record = cls(
            id=id,
            started_at=started_at,
            method=method,
            path=path,
            status=status,
            duration_ms=duration_ms,
            response_bytes=response_bytes,
            complete=complete,
            error=error,
        )

        dev_bridge_request_record.additional_properties = d
        return dev_bridge_request_record

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
