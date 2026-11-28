from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.invite_member_request_role import InviteMemberRequestRole, check_invite_member_request_role

T = TypeVar("T", bound="InviteMemberRequest")


@_attrs_define
class InviteMemberRequest:
    """POST /v1/orgs/{slug}/members body. Role cannot be `owner`.
    The handler mints a 32-byte plaintext token (returned ONCE
    in the response) and stores only the SHA-256 hash. The
    token expires after 14 days.

    """

    email: str
    role: InviteMemberRequestRole
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        email = self.email

        role: str = self.role

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "email": email,
                "role": role,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        email = d.pop("email")

        role = check_invite_member_request_role(d.pop("role"))

        invite_member_request = cls(
            email=email,
            role=role,
        )

        invite_member_request.additional_properties = d
        return invite_member_request

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
