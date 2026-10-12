from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_outbound_ai_route_request_format import (
    CreateOutboundAIRouteRequestFormat,
    check_create_outbound_ai_route_request_format,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_route_target import OutboundAIRouteTarget


T = TypeVar("T", bound="CreateOutboundAIRouteRequest")


@_attrs_define
class CreateOutboundAIRouteRequest:
    """A new AI route with a DNS-label name, an optional format and 1 to 5 ordered targets."""

    name: str
    targets: list[OutboundAIRouteTarget]
    format_: CreateOutboundAIRouteRequestFormat | Unset = UNSET
    """native (default) needs one ai_provider across targets; openai accepts openai, azure_openai, anthropic and
    gemini targets, and every target but openai must name its model (ADR-991)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        format_: str | Unset = UNSET
        if not isinstance(self.format_, Unset):
            format_ = self.format_

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "targets": targets,
            }
        )
        if format_ is not UNSET:
            field_dict["format"] = format_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_route_target import OutboundAIRouteTarget

        d = dict(src_dict)
        name = d.pop("name")

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = OutboundAIRouteTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        _format_ = d.pop("format", UNSET)
        format_: CreateOutboundAIRouteRequestFormat | Unset
        if isinstance(_format_, Unset):
            format_ = UNSET
        else:
            format_ = check_create_outbound_ai_route_request_format(_format_)

        create_outbound_ai_route_request = cls(
            name=name,
            targets=targets,
            format_=format_,
        )

        create_outbound_ai_route_request.additional_properties = d
        return create_outbound_ai_route_request

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
