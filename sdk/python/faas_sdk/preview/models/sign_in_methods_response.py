from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.linked_sign_in import LinkedSignIn


T = TypeVar("T", bound="SignInMethodsResponse")


@_attrs_define
class SignInMethodsResponse:
    """The ways the account can sign in."""

    password: bool
    mfa: bool
    providers: list[LinkedSignIn]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        password = self.password

        mfa = self.mfa

        providers = []
        for providers_item_data in self.providers:
            providers_item = providers_item_data.to_dict()
            providers.append(providers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "password": password,
                "mfa": mfa,
                "providers": providers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.linked_sign_in import LinkedSignIn

        d = dict(src_dict)
        password = d.pop("password")

        mfa = d.pop("mfa")

        providers = []
        _providers = d.pop("providers")
        for providers_item_data in _providers:
            providers_item = LinkedSignIn.from_dict(providers_item_data)

            providers.append(providers_item)

        sign_in_methods_response = cls(
            password=password,
            mfa=mfa,
            providers=providers,
        )

        sign_in_methods_response.additional_properties = d
        return sign_in_methods_response

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
