from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_content_body_encoding import (
    OutboundAIContentBodyEncoding,
    check_outbound_ai_content_body_encoding,
)

T = TypeVar("T", bound="OutboundAIContentBody")


@_attrs_define
class OutboundAIContentBody:
    """The start of one captured body."""

    body: str
    """The captured bytes as text, or base64 when they are not UTF-8."""
    encoding: OutboundAIContentBodyEncoding
    truncated: bool
    """True when the body was longer than the 32 KiB kept, or streamed without buffering."""
    content_type: None | str
    """The response Content-Type; null for requests."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        body = self.body

        encoding: str = self.encoding

        truncated = self.truncated

        content_type: None | str
        content_type = self.content_type

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "body": body,
                "encoding": encoding,
                "truncated": truncated,
                "content_type": content_type,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        body = d.pop("body")

        encoding = check_outbound_ai_content_body_encoding(d.pop("encoding"))

        truncated = d.pop("truncated")

        def _parse_content_type(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        content_type = _parse_content_type(d.pop("content_type"))

        outbound_ai_content_body = cls(
            body=body,
            encoding=encoding,
            truncated=truncated,
            content_type=content_type,
        )

        outbound_ai_content_body.additional_properties = d
        return outbound_ai_content_body

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
