from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.o_auth_provider_capability import OAuthProviderCapability


T = TypeVar("T", bound="AuthProviders")


@_attrs_define
class AuthProviders:
    """Per-provider capability map. Closed set (google, github) —
    handlers MUST add a new field here when adding a new
    provider, not relax this to a free-form map.

    """

    google: OAuthProviderCapability
    """One provider's capability flag. Source is
    `auth.SignInProvider.Enabled()` — the boot-resolved state.
    """
    github: OAuthProviderCapability
    """One provider's capability flag. Source is
    `auth.SignInProvider.Enabled()` — the boot-resolved state.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        google = self.google.to_dict()

        github = self.github.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "google": google,
                "github": github,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.o_auth_provider_capability import OAuthProviderCapability

        d = dict(src_dict)
        google = OAuthProviderCapability.from_dict(d.pop("google"))

        github = OAuthProviderCapability.from_dict(d.pop("github"))

        auth_providers = cls(
            google=google,
            github=github,
        )

        auth_providers.additional_properties = d
        return auth_providers

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
