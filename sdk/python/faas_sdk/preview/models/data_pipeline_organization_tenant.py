from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataPipelineOrganizationTenant")


@_attrs_define
class DataPipelineOrganizationTenant:
    """One authoritative Auth organization mapped to an exact source tenant value."""

    organization_id: UUID
    tenant: str

    def to_dict(self) -> dict[str, Any]:
        organization_id = str(self.organization_id)

        tenant = self.tenant

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "organization_id": organization_id,
                "tenant": tenant,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        organization_id = UUID(d.pop("organization_id"))

        tenant = d.pop("tenant")

        data_pipeline_organization_tenant = cls(
            organization_id=organization_id,
            tenant=tenant,
        )

        return data_pipeline_organization_tenant
