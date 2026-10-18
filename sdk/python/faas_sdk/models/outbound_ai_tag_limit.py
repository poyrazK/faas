from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAITagLimit")


@_attrs_define
class OutboundAITagLimit:
    """A UTC-day cost limit on one exact tag (ADR-1436)."""

    tag: str
    daily_cost_limit_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tag = self.tag

        daily_cost_limit_millicents = self.daily_cost_limit_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tag": tag,
                "daily_cost_limit_millicents": daily_cost_limit_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tag = d.pop("tag")

        daily_cost_limit_millicents = d.pop("daily_cost_limit_millicents")

        outbound_ai_tag_limit = cls(
            tag=tag,
            daily_cost_limit_millicents=daily_cost_limit_millicents,
        )

        outbound_ai_tag_limit.additional_properties = d
        return outbound_ai_tag_limit

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
