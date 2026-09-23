from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_secret_response_managed_by import (
    ProjectEnvironmentSecretResponseManagedBy,
    check_project_environment_secret_response_managed_by,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentSecretResponse")


@_attrs_define
class ProjectEnvironmentSecretResponse:
    """Safe secret metadata. Secret values and ciphertext are never included."""

    key: str
    value_hash: str | Unset = UNSET
    managed_by: ProjectEnvironmentSecretResponseManagedBy | Unset = UNSET
    binding_id: str | Unset = UNSET
    credential_generation: int | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        value_hash = self.value_hash

        managed_by: str | Unset = UNSET
        if not isinstance(self.managed_by, Unset):
            managed_by = self.managed_by

        binding_id = self.binding_id

        credential_generation = self.credential_generation

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
            }
        )
        if value_hash is not UNSET:
            field_dict["value_hash"] = value_hash
        if managed_by is not UNSET:
            field_dict["managed_by"] = managed_by
        if binding_id is not UNSET:
            field_dict["binding_id"] = binding_id
        if credential_generation is not UNSET:
            field_dict["credential_generation"] = credential_generation
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        value_hash = d.pop("value_hash", UNSET)

        _managed_by = d.pop("managed_by", UNSET)
        managed_by: ProjectEnvironmentSecretResponseManagedBy | Unset
        if isinstance(_managed_by, Unset):
            managed_by = UNSET
        else:
            managed_by = check_project_environment_secret_response_managed_by(_managed_by)

        binding_id = d.pop("binding_id", UNSET)

        credential_generation = d.pop("credential_generation", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        project_environment_secret_response = cls(
            key=key,
            value_hash=value_hash,
            managed_by=managed_by,
            binding_id=binding_id,
            credential_generation=credential_generation,
            updated_at=updated_at,
        )

        project_environment_secret_response.additional_properties = d
        return project_environment_secret_response

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
