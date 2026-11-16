from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIPromptTarget")


@_attrs_define
class OutboundAIPromptTarget:
    """Where a version's calls go: an openai-format route, or a managed AI integration with a model. Exactly one of
    route_id and integration_id is set; on a route target model is optional and is the request's model.

    """

    route_id: UUID | Unset = UNSET
    integration_id: UUID | Unset = UNSET
    model: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route_id: str | Unset = UNSET
        if not isinstance(self.route_id, Unset):
            route_id = str(self.route_id)

        integration_id: str | Unset = UNSET
        if not isinstance(self.integration_id, Unset):
            integration_id = str(self.integration_id)

        model = self.model

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if route_id is not UNSET:
            field_dict["route_id"] = route_id
        if integration_id is not UNSET:
            field_dict["integration_id"] = integration_id
        if model is not UNSET:
            field_dict["model"] = model

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _route_id = d.pop("route_id", UNSET)
        route_id: UUID | Unset
        if isinstance(_route_id, Unset):
            route_id = UNSET
        else:
            route_id = UUID(_route_id)

        _integration_id = d.pop("integration_id", UNSET)
        integration_id: UUID | Unset
        if isinstance(_integration_id, Unset):
            integration_id = UNSET
        else:
            integration_id = UUID(_integration_id)

        model = d.pop("model", UNSET)

        outbound_ai_prompt_target = cls(
            route_id=route_id,
            integration_id=integration_id,
            model=model,
        )

        outbound_ai_prompt_target.additional_properties = d
        return outbound_ai_prompt_target

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
