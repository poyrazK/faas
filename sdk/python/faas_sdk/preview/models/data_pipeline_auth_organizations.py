from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_organization_tenant import DataPipelineOrganizationTenant


T = TypeVar("T", bound="DataPipelineAuthOrganizations")


@_attrs_define
class DataPipelineAuthOrganizations:
    """Owner-approved Auth organization mapping. Internal binding identifiers are resolved by the control plane and
    rechecked on every snapshot.

    """

    auth_app_id: UUID
    mappings: list[DataPipelineOrganizationTenant]
    binding_id: UUID | Unset = UNSET
    database_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        auth_app_id = str(self.auth_app_id)

        mappings = []
        for mappings_item_data in self.mappings:
            mappings_item = mappings_item_data.to_dict()
            mappings.append(mappings_item)

        binding_id: str | Unset = UNSET
        if not isinstance(self.binding_id, Unset):
            binding_id = str(self.binding_id)

        database_id: str | Unset = UNSET
        if not isinstance(self.database_id, Unset):
            database_id = str(self.database_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "auth_app_id": auth_app_id,
                "mappings": mappings,
            }
        )
        if binding_id is not UNSET:
            field_dict["binding_id"] = binding_id
        if database_id is not UNSET:
            field_dict["database_id"] = database_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_organization_tenant import DataPipelineOrganizationTenant

        d = dict(src_dict)
        auth_app_id = UUID(d.pop("auth_app_id"))

        mappings = []
        _mappings = d.pop("mappings")
        for mappings_item_data in _mappings:
            mappings_item = DataPipelineOrganizationTenant.from_dict(mappings_item_data)

            mappings.append(mappings_item)

        _binding_id = d.pop("binding_id", UNSET)
        binding_id: UUID | Unset
        if isinstance(_binding_id, Unset):
            binding_id = UNSET
        else:
            binding_id = UUID(_binding_id)

        _database_id = d.pop("database_id", UNSET)
        database_id: UUID | Unset
        if isinstance(_database_id, Unset):
            database_id = UNSET
        else:
            database_id = UUID(_database_id)

        data_pipeline_auth_organizations = cls(
            auth_app_id=auth_app_id,
            mappings=mappings,
            binding_id=binding_id,
            database_id=database_id,
        )

        return data_pipeline_auth_organizations
