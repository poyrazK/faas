from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_outbound_integration_request_allowed_methods_item import (
    CreateOutboundIntegrationRequestAllowedMethodsItem,
    check_create_outbound_integration_request_allowed_methods_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateOutboundIntegrationRequest")


@_attrs_define
class CreateOutboundIntegrationRequest:
    """A fixed public HTTPS destination, maximum HTTP route policy, and optional daily admitted-request limit."""

    name: str
    origin: str
    allowed_methods: list[CreateOutboundIntegrationRequestAllowedMethodsItem]
    allowed_path_prefixes: list[str]
    daily_request_limit: int | None | Unset = UNSET
    """Optional per-integration daily admitted-request limit; account plan ceilings may be lower."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        origin = self.origin

        allowed_methods = []
        for allowed_methods_item_data in self.allowed_methods:
            allowed_methods_item: str = allowed_methods_item_data
            allowed_methods.append(allowed_methods_item)

        allowed_path_prefixes = self.allowed_path_prefixes

        daily_request_limit: int | None | Unset
        if isinstance(self.daily_request_limit, Unset):
            daily_request_limit = UNSET
        else:
            daily_request_limit = self.daily_request_limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "origin": origin,
                "allowed_methods": allowed_methods,
                "allowed_path_prefixes": allowed_path_prefixes,
            }
        )
        if daily_request_limit is not UNSET:
            field_dict["daily_request_limit"] = daily_request_limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        origin = d.pop("origin")

        allowed_methods = []
        _allowed_methods = d.pop("allowed_methods")
        for allowed_methods_item_data in _allowed_methods:
            allowed_methods_item = check_create_outbound_integration_request_allowed_methods_item(
                allowed_methods_item_data
            )

            allowed_methods.append(allowed_methods_item)

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes"))

        def _parse_daily_request_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        daily_request_limit = _parse_daily_request_limit(d.pop("daily_request_limit", UNSET))

        create_outbound_integration_request = cls(
            name=name,
            origin=origin,
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
            daily_request_limit=daily_request_limit,
        )

        create_outbound_integration_request.additional_properties = d
        return create_outbound_integration_request

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
