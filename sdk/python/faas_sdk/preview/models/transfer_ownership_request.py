from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="TransferOwnershipRequest")


@_attrs_define
class TransferOwnershipRequest:
    """POST /v1/orgs/{slug}/transfer_ownership body. The new owner
    must already be an active member of the org; the previous
    owner becomes admin on success. The exactly-one-owner
    invariant is enforced by the partial unique index
    `org_memberships_one_owner_idx` (migration 00099).

    """

    new_owner_account_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        new_owner_account_id = self.new_owner_account_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "new_owner_account_id": new_owner_account_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        new_owner_account_id = d.pop("new_owner_account_id")

        transfer_ownership_request = cls(
            new_owner_account_id=new_owner_account_id,
        )

        transfer_ownership_request.additional_properties = d
        return transfer_ownership_request

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
