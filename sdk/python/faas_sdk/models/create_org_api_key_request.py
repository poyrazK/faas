from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_org_api_key_request_scopes_item import (
    CreateOrgAPIKeyRequestScopesItem,
    check_create_org_api_key_request_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateOrgAPIKeyRequest")


@_attrs_define
class CreateOrgAPIKeyRequest:
    """POST /v1/orgs/{slug}/keys body. Mirrors `CreateKeyRequest` (PR 6 keeps them in lockstep) — label + optional scopes.
    Empty `scopes` defaults to `["admin"]` so existing callers preserve the legacy full-access behavior. See IAM-1,
    ADR-034 rev2.

    """

    label: str | Unset = UNSET
    scopes: list[CreateOrgAPIKeyRequestScopesItem] | Unset = UNSET
    """Requested permission set for the org-scoped key. Unknown scopes are rejected; object-storage data scopes
    also require an explicit logical-bucket grant. The legacy and org-scoped key vocabularies remain identical."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        scopes: list[str] | Unset = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = []
            for scopes_item_data in self.scopes:
                scopes_item: str = scopes_item_data
                scopes.append(scopes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if label is not UNSET:
            field_dict["label"] = label
        if scopes is not UNSET:
            field_dict["scopes"] = scopes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label", UNSET)

        _scopes = d.pop("scopes", UNSET)
        scopes: list[CreateOrgAPIKeyRequestScopesItem] | Unset = UNSET
        if _scopes is not UNSET:
            scopes = []
            for scopes_item_data in _scopes:
                scopes_item = check_create_org_api_key_request_scopes_item(scopes_item_data)

                scopes.append(scopes_item)

        create_org_api_key_request = cls(
            label=label,
            scopes=scopes,
        )

        create_org_api_key_request.additional_properties = d
        return create_org_api_key_request

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
