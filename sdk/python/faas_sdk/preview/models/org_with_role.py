from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.org_response_plan import OrgResponsePlan, check_org_response_plan
from ..models.org_response_status import OrgResponseStatus, check_org_response_status
from ..models.org_with_role_role import OrgWithRoleRole, check_org_with_role_role

T = TypeVar("T", bound="OrgWithRole")


@_attrs_define
class OrgWithRole:
    """OrgResponse + the caller's role on the active org. Used by
    GET /v1/orgs/me (`OrgMeResponse.org`). The role field is
    a closed enum: owner|admin|developer|viewer|billing.

    """

    id: str
    """Org UUID (stable across renames)."""
    slug: str
    """Lowercase slug. Personal orgs use `u-<12hex>`."""
    name: str
    personal: bool
    """True iff this is the caller's personal org (every account has exactly one)."""
    plan: OrgResponsePlan
    """Plan tier. Personal orgs default to `free`."""
    status: OrgResponseStatus
    created_at: datetime.datetime
    updated_at: datetime.datetime
    role: OrgWithRoleRole
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        slug = self.slug

        name = self.name

        personal = self.personal

        plan: str = self.plan

        status: str = self.status

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        role: str = self.role

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "slug": slug,
                "name": name,
                "personal": personal,
                "plan": plan,
                "status": status,
                "created_at": created_at,
                "updated_at": updated_at,
                "role": role,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        slug = d.pop("slug")

        name = d.pop("name")

        personal = d.pop("personal")

        plan = check_org_response_plan(d.pop("plan"))

        status = check_org_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        role = check_org_with_role_role(d.pop("role"))

        org_with_role = cls(
            id=id,
            slug=slug,
            name=name,
            personal=personal,
            plan=plan,
            status=status,
            created_at=created_at,
            updated_at=updated_at,
            role=role,
        )

        org_with_role.additional_properties = d
        return org_with_role

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
