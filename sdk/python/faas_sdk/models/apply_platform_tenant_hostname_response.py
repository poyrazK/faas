from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.apply_platform_tenant_hostname_response_action import (
    ApplyPlatformTenantHostnameResponseAction,
    check_apply_platform_tenant_hostname_response_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplyPlatformTenantHostnameResponse")


@_attrs_define
class ApplyPlatformTenantHostnameResponse:
    """Hostname reconciliation action and TXT challenge; new dry-run hostnames have no usable challenge token."""

    hostname: str
    verified: bool
    action: ApplyPlatformTenantHostnameResponseAction
    challenge_token: str | Unset = UNSET
    verified_at: datetime.datetime | Unset = UNSET
    last_error: str | Unset = UNSET
    txt_record: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        hostname = self.hostname

        verified = self.verified

        action: str = self.action

        challenge_token = self.challenge_token

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        last_error = self.last_error

        txt_record = self.txt_record

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "hostname": hostname,
                "verified": verified,
                "action": action,
            }
        )
        if challenge_token is not UNSET:
            field_dict["challenge_token"] = challenge_token
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if txt_record is not UNSET:
            field_dict["txt_record"] = txt_record

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        hostname = d.pop("hostname")

        verified = d.pop("verified")

        action = check_apply_platform_tenant_hostname_response_action(d.pop("action"))

        challenge_token = d.pop("challenge_token", UNSET)

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        last_error = d.pop("last_error", UNSET)

        txt_record = d.pop("txt_record", UNSET)

        apply_platform_tenant_hostname_response = cls(
            hostname=hostname,
            verified=verified,
            action=action,
            challenge_token=challenge_token,
            verified_at=verified_at,
            last_error=last_error,
            txt_record=txt_record,
        )

        apply_platform_tenant_hostname_response.additional_properties = d
        return apply_platform_tenant_hostname_response

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
