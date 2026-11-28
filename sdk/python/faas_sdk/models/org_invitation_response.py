from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.org_invitation_response_role import OrgInvitationResponseRole, check_org_invitation_response_role
from ..models.org_invitation_response_status import OrgInvitationResponseStatus, check_org_invitation_response_status

T = TypeVar("T", bound="OrgInvitationResponse")


@_attrs_define
class OrgInvitationResponse:
    """Wire shape for a single invitation row. Status is the
    runtime-derived value (pending | consumed | revoked |
    expired) computed from the (consumed_at, revoked_at,
    expires_at) tuple. Plaintext token is NEVER re-served —
    it's returned ONCE on the create call via
    InvitationWithTokenResponse.

    """

    id: str
    """32-hex opaque invitation id (NOT canonical UUID)."""
    org_id: str
    org_slug: str
    email: str
    role: OrgInvitationResponseRole
    status: OrgInvitationResponseStatus
    expires_at: datetime.datetime
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        org_id = self.org_id

        org_slug = self.org_slug

        email = self.email

        role: str = self.role

        status: str = self.status

        expires_at = self.expires_at.isoformat()

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "org_slug": org_slug,
                "email": email,
                "role": role,
                "status": status,
                "expires_at": expires_at,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        org_id = d.pop("org_id")

        org_slug = d.pop("org_slug")

        email = d.pop("email")

        role = check_org_invitation_response_role(d.pop("role"))

        status = check_org_invitation_response_status(d.pop("status"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        org_invitation_response = cls(
            id=id,
            org_id=org_id,
            org_slug=org_slug,
            email=email,
            role=role,
            status=status,
            expires_at=expires_at,
            created_at=created_at,
        )

        org_invitation_response.additional_properties = d
        return org_invitation_response

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
