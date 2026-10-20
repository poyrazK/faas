from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_self_hostname_response_action import (
    PlatformTenantSelfHostnameResponseAction,
    check_platform_tenant_self_hostname_response_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantSelfHostnameResponse")


@_attrs_define
class PlatformTenantSelfHostnameResponse:
    """A redacted hostname intent and DNS TXT challenge. DNS and certificate status remain asynchronous."""

    surface_id: UUID
    hostname: str
    action: PlatformTenantSelfHostnameResponseAction
    verified: bool
    txt_record: str
    """DNS TXT record name to publish for ownership proof."""
    verified_at: datetime.datetime | Unset = UNSET
    challenge_token: str | Unset = UNSET
    """Returned only while DNS ownership remains unverified; store securely and publish as the TXT value."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        surface_id = str(self.surface_id)

        hostname = self.hostname

        action: str = self.action

        verified = self.verified

        txt_record = self.txt_record

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        challenge_token = self.challenge_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "surface_id": surface_id,
                "hostname": hostname,
                "action": action,
                "verified": verified,
                "txt_record": txt_record,
            }
        )
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if challenge_token is not UNSET:
            field_dict["challenge_token"] = challenge_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        surface_id = UUID(d.pop("surface_id"))

        hostname = d.pop("hostname")

        action = check_platform_tenant_self_hostname_response_action(d.pop("action"))

        verified = d.pop("verified")

        txt_record = d.pop("txt_record")

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        challenge_token = d.pop("challenge_token", UNSET)

        platform_tenant_self_hostname_response = cls(
            surface_id=surface_id,
            hostname=hostname,
            action=action,
            verified=verified,
            txt_record=txt_record,
            verified_at=verified_at,
            challenge_token=challenge_token,
        )

        platform_tenant_self_hostname_response.additional_properties = d
        return platform_tenant_self_hostname_response

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
