from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_serving_version import DataPipelineServingVersion, check_data_pipeline_serving_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_auth_organizations import DataPipelineAuthOrganizations
    from ..models.data_pipeline_relationship import DataPipelineRelationship
    from ..models.data_pipeline_serving_columns import DataPipelineServingColumns
    from ..models.data_pipeline_tenant_access import DataPipelineTenantAccess


T = TypeVar("T", bound="DataPipelineServing")


@_attrs_define
class DataPipelineServing:
    """Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1; approved
    evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no memberships. Column
    expansion requires source-definition approval, backfill, verification and API refresh.

    """

    version: DataPipelineServingVersion | Unset = 1
    columns: DataPipelineServingColumns | Unset = UNSET
    """Every selected table maps to 1–128 unique columns, including all primary keys and the tenant column. Names
    are bounded to 63 UTF-8 bytes."""
    auth_organizations: DataPipelineAuthOrganizations | Unset = UNSET
    """Owner-approved Auth organization mapping. Internal binding identifiers are resolved by the control plane and
    rechecked on every snapshot."""
    relationships: list[DataPipelineRelationship] | Unset = UNSET
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

        auth_organizations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.auth_organizations, Unset):
            auth_organizations = self.auth_organizations.to_dict()

        relationships: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.relationships, Unset):
            relationships = []
            for relationships_item_data in self.relationships:
                relationships_item = relationships_item_data.to_dict()
                relationships.append(relationships_item)

        tenant_access: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tenant_access, Unset):
            tenant_access = self.tenant_access.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if version is not UNSET:
            field_dict["version"] = version
        if columns is not UNSET:
            field_dict["columns"] = columns
        if auth_organizations is not UNSET:
            field_dict["auth_organizations"] = auth_organizations
        if relationships is not UNSET:
            field_dict["relationships"] = relationships
        if tenant_access is not UNSET:
            field_dict["tenant_access"] = tenant_access

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_auth_organizations import DataPipelineAuthOrganizations
        from ..models.data_pipeline_relationship import DataPipelineRelationship
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

        _auth_organizations = d.pop("auth_organizations", UNSET)
        auth_organizations: DataPipelineAuthOrganizations | Unset
        if isinstance(_auth_organizations, Unset):
            auth_organizations = UNSET
        else:
            auth_organizations = DataPipelineAuthOrganizations.from_dict(_auth_organizations)

        _relationships = d.pop("relationships", UNSET)
        relationships: list[DataPipelineRelationship] | Unset = UNSET
        if _relationships is not UNSET:
            relationships = []
            for relationships_item_data in _relationships:
                relationships_item = DataPipelineRelationship.from_dict(relationships_item_data)

                relationships.append(relationships_item)

        _tenant_access = d.pop("tenant_access", UNSET)
        tenant_access: DataPipelineTenantAccess | Unset
        if isinstance(_tenant_access, Unset):
            tenant_access = UNSET
        else:
            tenant_access = DataPipelineTenantAccess.from_dict(_tenant_access)

        data_pipeline_serving = cls(
            version=version,
            columns=columns,
            auth_organizations=auth_organizations,
            relationships=relationships,
            tenant_access=tenant_access,
        )

        return data_pipeline_serving
