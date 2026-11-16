from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.tool_publication_record import ToolPublicationRecord


T = TypeVar("T", bound="BeginToolPublicationResponse")


@_attrs_define
class BeginToolPublicationResponse:
    """Journal insertion receipt; only a new insertion can authorize one publisher launch."""

    created: bool
    """True only for the first successful journal insertion; never cached as execution permission"""
    record: ToolPublicationRecord
    """Account-owned durable publication review and immutable outcome bound to native deployment source identity."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        created = self.created

        record = self.record.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "created": created,
                "record": record,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_record import ToolPublicationRecord

        d = dict(src_dict)
        created = d.pop("created")

        record = ToolPublicationRecord.from_dict(d.pop("record"))

        begin_tool_publication_response = cls(
            created=created,
            record=record,
        )

        begin_tool_publication_response.additional_properties = d
        return begin_tool_publication_response

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
