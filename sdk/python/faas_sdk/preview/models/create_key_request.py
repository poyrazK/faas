from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_key_request_approval_categories_item import (
    CreateKeyRequestApprovalCategoriesItem,
    check_create_key_request_approval_categories_item,
)
from ..models.create_key_request_scopes_item import CreateKeyRequestScopesItem, check_create_key_request_scopes_item
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateKeyRequest")


@_attrs_define
class CreateKeyRequest:
    """API key creation payload — label and optional scopes. Plaintext is returned exactly once in the 201 response. Scopes
    defaults to `["admin"]` when omitted so existing callers preserve the legacy full-access behavior. rev2.

    """

    label: str | Unset = UNSET
    scopes: list[CreateKeyRequestScopesItem] | Unset = UNSET
    """Requested permission set. The server rejects unknown scopes. github:manage controls customer GitHub
    connection stored workflow. Object-storage read/write scopes do not expose data until a storage manager grants
    the key access to a logical bucket."""
    projects: list[str] | Unset = UNSET
    """Project slugs the key is limited to. Only an agent key (scope agent) can be limited; it then reaches only
    apps in these projects and cannot carry Postgres scopes. Omit for every project."""
    approval_categories: list[CreateKeyRequestApprovalCategoriesItem] | Unset = UNSET
    """Kinds of action an agent key must have a person approve before Gregale carries out the request. Omit for
    every kind; send an empty list for none. Only agent keys take this field."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        scopes: list[str] | Unset = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = []
            for scopes_item_data in self.scopes:
                scopes_item: str = scopes_item_data
                scopes.append(scopes_item)

        projects: list[str] | Unset = UNSET
        if not isinstance(self.projects, Unset):
            projects = self.projects

        approval_categories: list[str] | Unset = UNSET
        if not isinstance(self.approval_categories, Unset):
            approval_categories = []
            for approval_categories_item_data in self.approval_categories:
                approval_categories_item: str = approval_categories_item_data
                approval_categories.append(approval_categories_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if label is not UNSET:
            field_dict["label"] = label
        if scopes is not UNSET:
            field_dict["scopes"] = scopes
        if projects is not UNSET:
            field_dict["projects"] = projects
        if approval_categories is not UNSET:
            field_dict["approval_categories"] = approval_categories

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label", UNSET)

        _scopes = d.pop("scopes", UNSET)
        scopes: list[CreateKeyRequestScopesItem] | Unset = UNSET
        if _scopes is not UNSET:
            scopes = []
            for scopes_item_data in _scopes:
                scopes_item = check_create_key_request_scopes_item(scopes_item_data)

                scopes.append(scopes_item)

        projects = cast(list[str], d.pop("projects", UNSET))

        _approval_categories = d.pop("approval_categories", UNSET)
        approval_categories: list[CreateKeyRequestApprovalCategoriesItem] | Unset = UNSET
        if _approval_categories is not UNSET:
            approval_categories = []
            for approval_categories_item_data in _approval_categories:
                approval_categories_item = check_create_key_request_approval_categories_item(
                    approval_categories_item_data
                )

                approval_categories.append(approval_categories_item)

        create_key_request = cls(
            label=label,
            scopes=scopes,
            projects=projects,
            approval_categories=approval_categories,
        )

        create_key_request.additional_properties = d
        return create_key_request

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
