from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_connection_check import DataConnectionCheck


T = TypeVar("T", bound="DataPipelineSetupPlan")


@_attrs_define
class DataPipelineSetupPlan:
    """Read-only resource and billing preview. No mirror or app is allocated and no capacity is reserved. Limits and
    billing implications are not a fixed-price invoice; source-provider compute charges are separate.

    """

    name: str
    creates_mirror: bool
    creates_data_api: bool
    billing_notes: list[str]
    existing_setup_id: UUID | Unset = UNSET
    database_id: UUID | Unset = UNSET
    region: str | Unset = UNSET
    storage_limit_bytes: int | Unset = UNSET
    restore_window_seconds: int | Unset = UNSET
    freshness_target_seconds: int | Unset = UNSET
    """DataPipelineSetupPlan uses zero to disable confirmation alerts; enabled targets must be at least 15 seconds."""
    connection_check: DataConnectionCheck | Unset = UNSET
    """Durable, bounded compatibility observation. Capacity separately reports measured, operator-qualified
    allocations; a passed compatibility check does not establish production readiness."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        creates_mirror = self.creates_mirror

        creates_data_api = self.creates_data_api

        billing_notes = self.billing_notes

        existing_setup_id: str | Unset = UNSET
        if not isinstance(self.existing_setup_id, Unset):
            existing_setup_id = str(self.existing_setup_id)

        database_id: str | Unset = UNSET
        if not isinstance(self.database_id, Unset):
            database_id = str(self.database_id)

        region = self.region

        storage_limit_bytes = self.storage_limit_bytes

        restore_window_seconds = self.restore_window_seconds

        freshness_target_seconds = self.freshness_target_seconds

        connection_check: dict[str, Any] | Unset = UNSET
        if not isinstance(self.connection_check, Unset):
            connection_check = self.connection_check.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "creates_mirror": creates_mirror,
                "creates_data_api": creates_data_api,
                "billing_notes": billing_notes,
            }
        )
        if existing_setup_id is not UNSET:
            field_dict["existing_setup_id"] = existing_setup_id
        if database_id is not UNSET:
            field_dict["database_id"] = database_id
        if region is not UNSET:
            field_dict["region"] = region
        if storage_limit_bytes is not UNSET:
            field_dict["storage_limit_bytes"] = storage_limit_bytes
        if restore_window_seconds is not UNSET:
            field_dict["restore_window_seconds"] = restore_window_seconds
        if freshness_target_seconds is not UNSET:
            field_dict["freshness_target_seconds"] = freshness_target_seconds
        if connection_check is not UNSET:
            field_dict["connection_check"] = connection_check

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_connection_check import DataConnectionCheck

        d = dict(src_dict)
        name = d.pop("name")

        creates_mirror = d.pop("creates_mirror")

        creates_data_api = d.pop("creates_data_api")

        billing_notes = cast(list[str], d.pop("billing_notes"))

        _existing_setup_id = d.pop("existing_setup_id", UNSET)
        existing_setup_id: UUID | Unset
        if isinstance(_existing_setup_id, Unset):
            existing_setup_id = UNSET
        else:
            existing_setup_id = UUID(_existing_setup_id)

        _database_id = d.pop("database_id", UNSET)
        database_id: UUID | Unset
        if isinstance(_database_id, Unset):
            database_id = UNSET
        else:
            database_id = UUID(_database_id)

        region = d.pop("region", UNSET)

        storage_limit_bytes = d.pop("storage_limit_bytes", UNSET)

        restore_window_seconds = d.pop("restore_window_seconds", UNSET)

        freshness_target_seconds = d.pop("freshness_target_seconds", UNSET)

        _connection_check = d.pop("connection_check", UNSET)
        connection_check: DataConnectionCheck | Unset
        if isinstance(_connection_check, Unset):
            connection_check = UNSET
        else:
            connection_check = DataConnectionCheck.from_dict(_connection_check)

        data_pipeline_setup_plan = cls(
            name=name,
            creates_mirror=creates_mirror,
            creates_data_api=creates_data_api,
            billing_notes=billing_notes,
            existing_setup_id=existing_setup_id,
            database_id=database_id,
            region=region,
            storage_limit_bytes=storage_limit_bytes,
            restore_window_seconds=restore_window_seconds,
            freshness_target_seconds=freshness_target_seconds,
            connection_check=connection_check,
        )

        return data_pipeline_setup_plan
