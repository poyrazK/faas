from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.org_membership_export_response_role import (
    OrgMembershipExportResponseRole,
    check_org_membership_export_response_role,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OrgMembershipExportResponse")


@_attrs_define
class OrgMembershipExportResponse:
    """Organization membership lifecycle row included in export schema v2."""

    org_id: UUID
    org_slug: str
    account_id: UUID
    email: str
    role: OrgMembershipExportResponseRole
    joined_at: datetime.datetime
    invited_by_account_id: UUID | Unset = UNSET
    removed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        org_id = str(self.org_id)

        org_slug = self.org_slug

        account_id = str(self.account_id)

        email = self.email

        role: str = self.role

        joined_at = self.joined_at.isoformat()

        invited_by_account_id: str | Unset = UNSET
        if not isinstance(self.invited_by_account_id, Unset):
            invited_by_account_id = str(self.invited_by_account_id)

        removed_at: str | Unset = UNSET
        if not isinstance(self.removed_at, Unset):
            removed_at = self.removed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "org_id": org_id,
                "org_slug": org_slug,
                "account_id": account_id,
                "email": email,
                "role": role,
                "joined_at": joined_at,
            }
        )
        if invited_by_account_id is not UNSET:
            field_dict["invited_by_account_id"] = invited_by_account_id
        if removed_at is not UNSET:
            field_dict["removed_at"] = removed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        org_id = UUID(d.pop("org_id"))

        org_slug = d.pop("org_slug")

        account_id = UUID(d.pop("account_id"))

        email = d.pop("email")

        role = check_org_membership_export_response_role(d.pop("role"))

        joined_at = datetime.datetime.fromisoformat(d.pop("joined_at"))

        _invited_by_account_id = d.pop("invited_by_account_id", UNSET)
        invited_by_account_id: UUID | Unset
        if isinstance(_invited_by_account_id, Unset):
            invited_by_account_id = UNSET
        else:
            invited_by_account_id = UUID(_invited_by_account_id)

        _removed_at = d.pop("removed_at", UNSET)
        removed_at: datetime.datetime | Unset
        if isinstance(_removed_at, Unset):
            removed_at = UNSET
        else:
            removed_at = datetime.datetime.fromisoformat(_removed_at)

        org_membership_export_response = cls(
            org_id=org_id,
            org_slug=org_slug,
            account_id=account_id,
            email=email,
            role=role,
            joined_at=joined_at,
            invited_by_account_id=invited_by_account_id,
            removed_at=removed_at,
        )

        org_membership_export_response.additional_properties = d
        return org_membership_export_response

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
