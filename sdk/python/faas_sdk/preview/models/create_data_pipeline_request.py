from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_serving import DataPipelineServing


T = TypeVar("T", bound="CreateDataPipelineRequest")


@_attrs_define
class CreateDataPipelineRequest:
    """Selected public tables, dedicated destination and explicitly authorized JWT readers. Supply read_subjects for whole-
    table access, or serving.tenant_access with owner-managed memberships and no read_subjects. Explicit columns must
    cover every table and retain all primary-key and tenant columns. A fresh passing connection check for the current
    source credential and all selected tables is required; otherwise returns data_connection_check_required.

    """

    name: str
    source_connection_id: UUID
    database_id: UUID
    tables: list[str]
    freshness_target_seconds: int | Unset = UNSET
    """Initial checkpoint confirmation target for the new pipeline; use zero to disable it, or 15 to 86400 seconds."""
    serving: DataPipelineServing | Unset = UNSET
    """Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1;
    approved evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no
    memberships. Column expansion requires source-definition approval, backfill, verification and API refresh."""
    read_subjects: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        source_connection_id = str(self.source_connection_id)

        database_id = str(self.database_id)

        tables = self.tables

        freshness_target_seconds = self.freshness_target_seconds

        serving: dict[str, Any] | Unset = UNSET
        if not isinstance(self.serving, Unset):
            serving = self.serving.to_dict()

        read_subjects: list[str] | Unset = UNSET
        if not isinstance(self.read_subjects, Unset):
            read_subjects = self.read_subjects

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "source_connection_id": source_connection_id,
                "database_id": database_id,
                "tables": tables,
            }
        )
        if freshness_target_seconds is not UNSET:
            field_dict["freshness_target_seconds"] = freshness_target_seconds
        if serving is not UNSET:
            field_dict["serving"] = serving
        if read_subjects is not UNSET:
            field_dict["read_subjects"] = read_subjects

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_serving import DataPipelineServing

        d = dict(src_dict)
        name = d.pop("name")

        source_connection_id = UUID(d.pop("source_connection_id"))

        database_id = UUID(d.pop("database_id"))

        tables = cast(list[str], d.pop("tables"))

        freshness_target_seconds = d.pop("freshness_target_seconds", UNSET)

        _serving = d.pop("serving", UNSET)
        serving: DataPipelineServing | Unset
        if isinstance(_serving, Unset):
            serving = UNSET
        else:
            serving = DataPipelineServing.from_dict(_serving)

        read_subjects = cast(list[str], d.pop("read_subjects", UNSET))

        create_data_pipeline_request = cls(
            name=name,
            source_connection_id=source_connection_id,
            database_id=database_id,
            tables=tables,
            freshness_target_seconds=freshness_target_seconds,
            serving=serving,
            read_subjects=read_subjects,
        )

        return create_data_pipeline_request
