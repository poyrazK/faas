from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_app_owner_response_kind import GlobalAppOwnerResponseKind, check_global_app_owner_response_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="GlobalAppOwnerResponse")


@_attrs_define
class GlobalAppOwnerResponse:
    """Immutable directory tenant root. Account roots retain the account UUID; organization roots have an independent UUID
    and survive creator departure.

    """

    id: UUID
    kind: GlobalAppOwnerResponseKind
    account_id: UUID | Unset = UNSET
    """Present only for account ownership."""
    org_id: UUID | Unset = UNSET
    """Present only for organization ownership."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        kind: str = self.kind

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        org_id: str | Unset = UNSET
        if not isinstance(self.org_id, Unset):
            org_id = str(self.org_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
            }
        )
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if org_id is not UNSET:
            field_dict["org_id"] = org_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        kind = check_global_app_owner_response_kind(d.pop("kind"))

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        _org_id = d.pop("org_id", UNSET)
        org_id: UUID | Unset
        if isinstance(_org_id, Unset):
            org_id = UNSET
        else:
            org_id = UUID(_org_id)

        global_app_owner_response = cls(
            id=id,
            kind=kind,
            account_id=account_id,
            org_id=org_id,
        )

        global_app_owner_response.additional_properties = d
        return global_app_owner_response

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
