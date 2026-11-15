from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_serving import DataPipelineServing


T = TypeVar("T", bound="DataPipelineSetupRequest")


@_attrs_define
class DataPipelineSetupRequest:
    """Immutable durable setup intent. Repeating exactly the same normalized name and selections recovers the saved job and
    resource IDs. Source discovery must already include the selected tables; the worker refreshes credentials and
    capacity before allocating resources.

    """

    name: str
    source_connection_id: UUID
    tables: list[str]
    database_id: UUID | Unset = UNSET
    region: str | Unset = UNSET
    read_subjects: list[str] | Unset = UNSET
    serving: DataPipelineServing | Unset = UNSET
    """Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1;
    approved evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no
    memberships. Column expansion requires source-definition approval, backfill, verification and API refresh."""
    freshness_target_seconds: int | Unset = UNSET
    """DataPipelineSetupRequest uses zero to disable confirmation alerts; enabled targets must be at least 15
    seconds."""
    data_api_name: str | Unset = UNSET
    auth_app_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        source_connection_id = str(self.source_connection_id)

        tables = self.tables

        database_id: str | Unset = UNSET
        if not isinstance(self.database_id, Unset):
            database_id = str(self.database_id)

        region = self.region

        read_subjects: list[str] | Unset = UNSET
        if not isinstance(self.read_subjects, Unset):
            read_subjects = self.read_subjects

        serving: dict[str, Any] | Unset = UNSET
        if not isinstance(self.serving, Unset):
            serving = self.serving.to_dict()

        freshness_target_seconds = self.freshness_target_seconds

        data_api_name = self.data_api_name

        auth_app_id: str | Unset = UNSET
        if not isinstance(self.auth_app_id, Unset):
            auth_app_id = str(self.auth_app_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "source_connection_id": source_connection_id,
                "tables": tables,
            }
        )
        if database_id is not UNSET:
            field_dict["database_id"] = database_id
        if region is not UNSET:
            field_dict["region"] = region
        if read_subjects is not UNSET:
            field_dict["read_subjects"] = read_subjects
        if serving is not UNSET:
            field_dict["serving"] = serving
        if freshness_target_seconds is not UNSET:
            field_dict["freshness_target_seconds"] = freshness_target_seconds
        if data_api_name is not UNSET:
            field_dict["data_api_name"] = data_api_name
        if auth_app_id is not UNSET:
            field_dict["auth_app_id"] = auth_app_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_serving import DataPipelineServing

        d = dict(src_dict)
        name = d.pop("name")

        source_connection_id = UUID(d.pop("source_connection_id"))

        tables = cast(list[str], d.pop("tables"))

        _database_id = d.pop("database_id", UNSET)
        database_id: UUID | Unset
        if isinstance(_database_id, Unset):
            database_id = UNSET
        else:
            database_id = UUID(_database_id)

        region = d.pop("region", UNSET)

        read_subjects = cast(list[str], d.pop("read_subjects", UNSET))

        _serving = d.pop("serving", UNSET)
        serving: DataPipelineServing | Unset
        if isinstance(_serving, Unset):
            serving = UNSET
        else:
            serving = DataPipelineServing.from_dict(_serving)

        freshness_target_seconds = d.pop("freshness_target_seconds", UNSET)

        data_api_name = d.pop("data_api_name", UNSET)

        _auth_app_id = d.pop("auth_app_id", UNSET)
        auth_app_id: UUID | Unset
        if isinstance(_auth_app_id, Unset):
            auth_app_id = UNSET
        else:
            auth_app_id = UUID(_auth_app_id)

        data_pipeline_setup_request = cls(
            name=name,
            source_connection_id=source_connection_id,
            tables=tables,
            database_id=database_id,
            region=region,
            read_subjects=read_subjects,
            serving=serving,
            freshness_target_seconds=freshness_target_seconds,
            data_api_name=data_api_name,
            auth_app_id=auth_app_id,
        )

        return data_pipeline_setup_request
