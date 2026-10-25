from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.org_invitation_response import OrgInvitationResponse


T = TypeVar("T", bound="InvitationListResponse")


@_attrs_define
class InvitationListResponse:
    """GET /v1/orgs/{slug}/invitations response. Sorted by created_at DESC."""

    invitations: list[OrgInvitationResponse]
    next_before: str | Unset = UNSET
    """Opaque cursor — set to the **base64-url-of-JSON** of
    the last row's compound `(created_at, id)` key when
    there's a next page. Pass back as `?before=` to
    fetch it. PR-9 changes the encoding from the v1
    bare-id cursor to the compound key so the
    `created_at DESC, id DESC` order is preserved
    exactly under random UUIDs. The wire shape is
    opaque; clients MUST pass the value back unchanged.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invitations = []
        for invitations_item_data in self.invitations:
            invitations_item = invitations_item_data.to_dict()
            invitations.append(invitations_item)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invitations": invitations,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.org_invitation_response import OrgInvitationResponse

        d = dict(src_dict)
        invitations = []
        _invitations = d.pop("invitations")
        for invitations_item_data in _invitations:
            invitations_item = OrgInvitationResponse.from_dict(invitations_item_data)

            invitations.append(invitations_item)

        next_before = d.pop("next_before", UNSET)

        invitation_list_response = cls(
            invitations=invitations,
            next_before=next_before,
        )

        invitation_list_response.additional_properties = d
        return invitation_list_response

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
