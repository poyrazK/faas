from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugEvidenceLogLine")


@_attrs_define
class DebugEvidenceLogLine:
    """One log line attached to request evidence. The message is bounded to 512 bytes; structured log fields are not
    included.

    """

    occurred_at: datetime.datetime
    source: str
    message: str
    level: str | Unset = UNSET
    stream: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        occurred_at = self.occurred_at.isoformat()

        source = self.source

        message = self.message

        level = self.level

        stream = self.stream

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "occurred_at": occurred_at,
                "source": source,
                "message": message,
            }
        )
        if level is not UNSET:
            field_dict["level"] = level
        if stream is not UNSET:
            field_dict["stream"] = stream

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        source = d.pop("source")

        message = d.pop("message")

        level = d.pop("level", UNSET)

        stream = d.pop("stream", UNSET)

        debug_evidence_log_line = cls(
            occurred_at=occurred_at,
            source=source,
            message=message,
            level=level,
            stream=stream,
        )

        debug_evidence_log_line.additional_properties = d
        return debug_evidence_log_line

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
