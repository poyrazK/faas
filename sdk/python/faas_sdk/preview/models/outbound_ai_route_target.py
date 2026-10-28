from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIRouteTarget")


@_attrs_define
class OutboundAIRouteTarget:
    """One ordered fallback step; model replaces the request's "model" for this target."""

    integration_id: UUID
    model: str | Unset = UNSET
    weight: int | Unset = UNSET
    """A weighted route's share of first attempts for this target; 0 makes it a fallback only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        integration_id = str(self.integration_id)

        model = self.model

        weight = self.weight

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "integration_id": integration_id,
            }
        )
        if model is not UNSET:
            field_dict["model"] = model
        if weight is not UNSET:
            field_dict["weight"] = weight

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        integration_id = UUID(d.pop("integration_id"))

        model = d.pop("model", UNSET)

        weight = d.pop("weight", UNSET)

        outbound_ai_route_target = cls(
            integration_id=integration_id,
            model=model,
            weight=weight,
        )

        outbound_ai_route_target.additional_properties = d
        return outbound_ai_route_target

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
