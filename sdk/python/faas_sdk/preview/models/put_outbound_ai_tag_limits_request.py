from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_tag_limit import OutboundAITagLimit


T = TypeVar("T", bound="PutOutboundAITagLimitsRequest")


@_attrs_define
class PutOutboundAITagLimitsRequest:
    """The complete list of tag limits; an empty list removes them."""

    limits: list[OutboundAITagLimit]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        limits = []
        for limits_item_data in self.limits:
            limits_item = limits_item_data.to_dict()
            limits.append(limits_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "limits": limits,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_tag_limit import OutboundAITagLimit

        d = dict(src_dict)
        limits = []
        _limits = d.pop("limits")
        for limits_item_data in _limits:
            limits_item = OutboundAITagLimit.from_dict(limits_item_data)

            limits.append(limits_item)

        put_outbound_ai_tag_limits_request = cls(
            limits=limits,
        )

        put_outbound_ai_tag_limits_request.additional_properties = d
        return put_outbound_ai_tag_limits_request

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
