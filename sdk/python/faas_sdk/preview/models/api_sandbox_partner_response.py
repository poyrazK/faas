from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxPartnerResponse")


@_attrs_define
class APISandboxPartnerResponse:
    """Owner-only partner progress."""

    label: str
    invited_at: datetime.datetime | Unset = UNSET
    redeemed_at: datetime.datetime | Unset = UNSET
    revoked_at: datetime.datetime | Unset = UNSET
    first_success_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        invited_at: str | Unset = UNSET
        if not isinstance(self.invited_at, Unset):
            invited_at = self.invited_at.isoformat()

        redeemed_at: str | Unset = UNSET
        if not isinstance(self.redeemed_at, Unset):
            redeemed_at = self.redeemed_at.isoformat()

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        first_success_at: str | Unset = UNSET
        if not isinstance(self.first_success_at, Unset):
            first_success_at = self.first_success_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "label": label,
            }
        )
        if invited_at is not UNSET:
            field_dict["invited_at"] = invited_at
        if redeemed_at is not UNSET:
            field_dict["redeemed_at"] = redeemed_at
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at
        if first_success_at is not UNSET:
            field_dict["first_success_at"] = first_success_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        _invited_at = d.pop("invited_at", UNSET)
        invited_at: datetime.datetime | Unset
        if isinstance(_invited_at, Unset):
            invited_at = UNSET
        else:
            invited_at = datetime.datetime.fromisoformat(_invited_at)

        _redeemed_at = d.pop("redeemed_at", UNSET)
        redeemed_at: datetime.datetime | Unset
        if isinstance(_redeemed_at, Unset):
            redeemed_at = UNSET
        else:
            redeemed_at = datetime.datetime.fromisoformat(_redeemed_at)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        _first_success_at = d.pop("first_success_at", UNSET)
        first_success_at: datetime.datetime | Unset
        if isinstance(_first_success_at, Unset):
            first_success_at = UNSET
        else:
            first_success_at = datetime.datetime.fromisoformat(_first_success_at)

        api_sandbox_partner_response = cls(
            label=label,
            invited_at=invited_at,
            redeemed_at=redeemed_at,
            revoked_at=revoked_at,
            first_success_at=first_success_at,
        )

        api_sandbox_partner_response.additional_properties = d
        return api_sandbox_partner_response

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
