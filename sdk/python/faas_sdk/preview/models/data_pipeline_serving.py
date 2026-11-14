from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_serving_version import DataPipelineServingVersion, check_data_pipeline_serving_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_serving_columns import DataPipelineServingColumns
    from ..models.data_pipeline_tenant_access import DataPipelineTenantAccess


T = TypeVar("T", bound="DataPipelineServing")


@_attrs_define
class DataPipelineServing:
    """Versioned serving contract. Columns are immutable and affect publication, mirror schema and recovery. Omit columns
    for all-column replication. Tenant access uses owner-managed subject memberships; JWT tenant claims grant nothing.
    At least columns or tenant_access is required. A missing version defaults to 1 at creation.

    """

    version: DataPipelineServingVersion | Unset = 1
    columns: DataPipelineServingColumns | Unset = UNSET
    """Every selected table maps to 1–128 unique columns, including all primary keys and the tenant column. Names
    are bounded to 63 UTF-8 bytes."""
    tenant_access: DataPipelineTenantAccess | Unset = UNSET
    """Row access through a text, varchar or UUID column present on every selected table. Memberships match
    verified JWT sub values exactly. Empty memberships revoke every reader."""

    def to_dict(self) -> dict[str, Any]:
        version: int | Unset = UNSET
        if not isinstance(self.version, Unset):
            version = self.version

        columns: dict[str, Any] | Unset = UNSET
        if not isinstance(self.columns, Unset):
            columns = self.columns.to_dict()

        tenant_access: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tenant_access, Unset):
            tenant_access = self.tenant_access.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if version is not UNSET:
            field_dict["version"] = version
        if columns is not UNSET:
            field_dict["columns"] = columns
        if tenant_access is not UNSET:
            field_dict["tenant_access"] = tenant_access

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_serving_columns import DataPipelineServingColumns
        from ..models.data_pipeline_tenant_access import DataPipelineTenantAccess

        d = dict(src_dict)
        _version = d.pop("version", UNSET)
        version: DataPipelineServingVersion | Unset
        if isinstance(_version, Unset):
            version = UNSET
        else:
            version = check_data_pipeline_serving_version(_version)

        _columns = d.pop("columns", UNSET)
        columns: DataPipelineServingColumns | Unset
        if isinstance(_columns, Unset):
            columns = UNSET
        else:
            columns = DataPipelineServingColumns.from_dict(_columns)

        _tenant_access = d.pop("tenant_access", UNSET)
        tenant_access: DataPipelineTenantAccess | Unset
        if isinstance(_tenant_access, Unset):
            tenant_access = UNSET
        else:
            tenant_access = DataPipelineTenantAccess.from_dict(_tenant_access)

        data_pipeline_serving = cls(
            version=version,
            columns=columns,
            tenant_access=tenant_access,
        )

        return data_pipeline_serving
