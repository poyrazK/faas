from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.oidc_exchange_request_capability import (
    OIDCExchangeRequestCapability,
    check_oidc_exchange_request_capability,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OIDCExchangeRequest")


@_attrs_define
class OIDCExchangeRequest:
    """Body for `POST /v1/auth/oidc/exchange` (ADR-101)."""

    provider: str
    """IdP identifier (`github`, `gitlab`, `circleci`, or generic `oidc`). Used for audit attribution only — the
    issuer is pinned in the JWT `iss` claim."""
    token: str
    """Raw IdP-issued JWT (the IdP token from `ACTIONS_ID_TOKEN_REQUEST_TOKEN` etc.)."""
    aud: str
    """The `aud` claim the customer pinned in the action. Must match the trust policy's `audience` array verbatim."""
    app: str | Unset = UNSET
    """Optional app slug for audit attribution. Empty skips the audit app attribution."""
    capability: OIDCExchangeRequestCapability | Unset = UNSET
    """Optional closed OIDC capability profile. Omit to retain the historical deploy:write bearer; environment-
    preflight grants only project-environment read and qualification scopes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider = self.provider

        token = self.token

        aud = self.aud

        app = self.app

        capability: str | Unset = UNSET
        if not isinstance(self.capability, Unset):
            capability = self.capability

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "token": token,
                "aud": aud,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if capability is not UNSET:
            field_dict["capability"] = capability

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = d.pop("provider")

        token = d.pop("token")

        aud = d.pop("aud")

        app = d.pop("app", UNSET)

        _capability = d.pop("capability", UNSET)
        capability: OIDCExchangeRequestCapability | Unset
        if isinstance(_capability, Unset):
            capability = UNSET
        else:
            capability = check_oidc_exchange_request_capability(_capability)

        oidc_exchange_request = cls(
            provider=provider,
            token=token,
            aud=aud,
            app=app,
            capability=capability,
        )

        oidc_exchange_request.additional_properties = d
        return oidc_exchange_request

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
