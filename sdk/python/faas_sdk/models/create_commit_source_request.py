from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_commit_source_request_contract_version import (
    CreateCommitSourceRequestContractVersion,
    check_create_commit_source_request_contract_version,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateCommitSourceRequest")


@_attrs_define
class CreateCommitSourceRequest:
    """Register an immutable database source bound to a managed app operation policy and routing contract."""

    name: str
    operation_policy: str
    """Active queue policy containing this application. Scope must be account or platform_tenant according to
    allow_tenant_selection. Environment-scoped policies are unsupported."""
    contract_version: CreateCommitSourceRequestContractVersion | Unset = 1
    """Immutable contract. Version 1 uses one source lane; version 2 requires explicit event routing and a business
    key."""
    allow_tenant_selection: bool | Unset = False
    """Immutable owner grant authorizing this trusted database to select active customers linked to the fixed
    target application. Requires contract version 2 and a platform_tenant queue policy."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        operation_policy = self.operation_policy

        contract_version: int | Unset = UNSET
        if not isinstance(self.contract_version, Unset):
            contract_version = self.contract_version

        allow_tenant_selection = self.allow_tenant_selection

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "operation_policy": operation_policy,
            }
        )
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version
        if allow_tenant_selection is not UNSET:
            field_dict["allow_tenant_selection"] = allow_tenant_selection

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        operation_policy = d.pop("operation_policy")

        _contract_version = d.pop("contract_version", UNSET)
        contract_version: CreateCommitSourceRequestContractVersion | Unset
        if isinstance(_contract_version, Unset):
            contract_version = UNSET
        else:
            contract_version = check_create_commit_source_request_contract_version(_contract_version)

        allow_tenant_selection = d.pop("allow_tenant_selection", UNSET)

        create_commit_source_request = cls(
            name=name,
            operation_policy=operation_policy,
            contract_version=contract_version,
            allow_tenant_selection=allow_tenant_selection,
        )

        create_commit_source_request.additional_properties = d
        return create_commit_source_request

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
