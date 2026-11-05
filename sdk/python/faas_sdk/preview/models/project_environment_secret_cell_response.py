from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_secret_cell_response_managed_by import (
    ProjectEnvironmentSecretCellResponseManagedBy,
    check_project_environment_secret_cell_response_managed_by,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentSecretCellResponse")


@_attrs_define
class ProjectEnvironmentSecretCellResponse:
    """One side of a secret comparison. Version is omitted when unknown; never contains secret material."""

    present: bool
    value_hash: str | Unset = UNSET
    version: int | Unset = UNSET
    managed_by: ProjectEnvironmentSecretCellResponseManagedBy | Unset = UNSET
    binding_id: str | Unset = UNSET
    credential_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        present = self.present

        value_hash = self.value_hash

        version = self.version

        managed_by: str | Unset = UNSET
        if not isinstance(self.managed_by, Unset):
            managed_by = self.managed_by

        binding_id = self.binding_id

        credential_generation = self.credential_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "present": present,
            }
        )
        if value_hash is not UNSET:
            field_dict["value_hash"] = value_hash
        if version is not UNSET:
            field_dict["version"] = version
        if managed_by is not UNSET:
            field_dict["managed_by"] = managed_by
        if binding_id is not UNSET:
            field_dict["binding_id"] = binding_id
        if credential_generation is not UNSET:
            field_dict["credential_generation"] = credential_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        present = d.pop("present")

        value_hash = d.pop("value_hash", UNSET)

        version = d.pop("version", UNSET)

        _managed_by = d.pop("managed_by", UNSET)
        managed_by: ProjectEnvironmentSecretCellResponseManagedBy | Unset
        if isinstance(_managed_by, Unset):
            managed_by = UNSET
        else:
            managed_by = check_project_environment_secret_cell_response_managed_by(_managed_by)

        binding_id = d.pop("binding_id", UNSET)

        credential_generation = d.pop("credential_generation", UNSET)

        project_environment_secret_cell_response = cls(
            present=present,
            value_hash=value_hash,
            version=version,
            managed_by=managed_by,
            binding_id=binding_id,
            credential_generation=credential_generation,
        )

        project_environment_secret_cell_response.additional_properties = d
        return project_environment_secret_cell_response

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
