from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_sandbox_partner_response import APISandboxPartnerResponse


T = TypeVar("T", bound="APISandboxPartnerInviteResponse")


@_attrs_define
class APISandboxPartnerInviteResponse:
    """One-time credential used to exchange an invitation for a partner key."""

    partner: APISandboxPartnerResponse
    """Owner-only partner progress."""
    token: str
    """One-time invite token exchanged through the sandbox redemption endpoint."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        partner = self.partner.to_dict()

        token = self.token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "partner": partner,
                "token": token,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_partner_response import APISandboxPartnerResponse

        d = dict(src_dict)
        partner = APISandboxPartnerResponse.from_dict(d.pop("partner"))

        token = d.pop("token")

        api_sandbox_partner_invite_response = cls(
            partner=partner,
            token=token,
        )

        api_sandbox_partner_invite_response.additional_properties = d
        return api_sandbox_partner_invite_response

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
