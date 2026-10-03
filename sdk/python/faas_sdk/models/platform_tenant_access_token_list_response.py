from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_access_token_response import PlatformTenantAccessTokenResponse


T = TypeVar("T", bound="PlatformTenantAccessTokenListResponse")


@_attrs_define
class PlatformTenantAccessTokenListResponse:
    """Tenant access-token metadata without any recoverable plaintext bearer."""

    tokens: list[PlatformTenantAccessTokenResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tokens = []
        for tokens_item_data in self.tokens:
            tokens_item = tokens_item_data.to_dict()
            tokens.append(tokens_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tokens": tokens,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_access_token_response import PlatformTenantAccessTokenResponse

        d = dict(src_dict)
        tokens = []
        _tokens = d.pop("tokens")
        for tokens_item_data in _tokens:
            tokens_item = PlatformTenantAccessTokenResponse.from_dict(tokens_item_data)

            tokens.append(tokens_item)

        platform_tenant_access_token_list_response = cls(
            tokens=tokens,
        )

        platform_tenant_access_token_list_response.additional_properties = d
        return platform_tenant_access_token_list_response

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
