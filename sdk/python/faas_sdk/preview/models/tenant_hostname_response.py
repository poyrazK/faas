from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="TenantHostnameResponse")


@_attrs_define
class TenantHostnameResponse:
    """A hostname attached to a tenant surface (DNS-01 verified). managed_by_platform_tenant is true only when created by a
    platform-tenant bundle apply.

    """

    hostname: str
    verified: bool
    txt_record: str
    """TXT record the customer must publish (_faas-verify.<hostname>)."""
    challenge_token: None | str | Unset = UNSET
    verified_at: datetime.datetime | None | Unset = UNSET
    last_error: None | str | Unset = UNSET
    managed_by_platform_tenant: bool | Unset = UNSET
    """True only when a platform-tenant bundle created this hostname; omitted otherwise."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        hostname = self.hostname

        verified = self.verified

        txt_record = self.txt_record

        challenge_token: None | str | Unset
        if isinstance(self.challenge_token, Unset):
            challenge_token = UNSET
        else:
            challenge_token = self.challenge_token

        verified_at: None | str | Unset
        if isinstance(self.verified_at, Unset):
            verified_at = UNSET
        elif isinstance(self.verified_at, datetime.datetime):
            verified_at = self.verified_at.isoformat()
        else:
            verified_at = self.verified_at

        last_error: None | str | Unset
        if isinstance(self.last_error, Unset):
            last_error = UNSET
        else:
            last_error = self.last_error

        managed_by_platform_tenant = self.managed_by_platform_tenant

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "hostname": hostname,
                "verified": verified,
                "txt_record": txt_record,
            }
        )
        if challenge_token is not UNSET:
            field_dict["challenge_token"] = challenge_token
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if managed_by_platform_tenant is not UNSET:
            field_dict["managed_by_platform_tenant"] = managed_by_platform_tenant

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        hostname = d.pop("hostname")

        verified = d.pop("verified")

        txt_record = d.pop("txt_record")

        def _parse_challenge_token(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        challenge_token = _parse_challenge_token(d.pop("challenge_token", UNSET))

        def _parse_verified_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verified_at_type_0 = datetime.datetime.fromisoformat(data)

                return verified_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        verified_at = _parse_verified_at(d.pop("verified_at", UNSET))

        def _parse_last_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        last_error = _parse_last_error(d.pop("last_error", UNSET))

        managed_by_platform_tenant = d.pop("managed_by_platform_tenant", UNSET)

        tenant_hostname_response = cls(
            hostname=hostname,
            verified=verified,
            txt_record=txt_record,
            challenge_token=challenge_token,
            verified_at=verified_at,
            last_error=last_error,
            managed_by_platform_tenant=managed_by_platform_tenant,
        )

        tenant_hostname_response.additional_properties = d
        return tenant_hostname_response

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
