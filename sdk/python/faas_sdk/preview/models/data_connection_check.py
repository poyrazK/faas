from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.data_connection_check_state import DataConnectionCheckState, check_data_connection_check_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_connection_issue import DataConnectionIssue
    from ..models.data_pipeline_capacity import DataPipelineCapacity
    from ..models.data_source_table import DataSourceTable


T = TypeVar("T", bound="DataConnectionCheck")


@_attrs_define
class DataConnectionCheck:
    """Durable, bounded compatibility observation. Capacity separately reports measured, operator-qualified allocations; a
    passed compatibility check does not establish production readiness.

    """

    id: UUID
    connection_id: UUID
    connection_revision: int
    state: DataConnectionCheckState
    fresh: bool
    truncated: bool
    checks: list[DataConnectionIssue]
    tables: list[DataSourceTable]
    created_at: datetime.datetime
    capacity: DataPipelineCapacity | Unset = UNSET
    """New-allocation readiness at observation time. Admission atomically rechecks and reserves capacity;
    observations alone reserve nothing."""
    database_id: UUID | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        connection_id = str(self.connection_id)

        connection_revision = self.connection_revision

        state: str = self.state

        fresh = self.fresh

        truncated = self.truncated

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        tables = []
        for tables_item_data in self.tables:
            tables_item = tables_item_data.to_dict()
            tables.append(tables_item)

        created_at = self.created_at.isoformat()

        capacity: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity, Unset):
            capacity = self.capacity.to_dict()

        database_id: str | Unset = UNSET
        if not isinstance(self.database_id, Unset):
            database_id = str(self.database_id)

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "connection_id": connection_id,
                "connection_revision": connection_revision,
                "state": state,
                "fresh": fresh,
                "truncated": truncated,
                "checks": checks,
                "tables": tables,
                "created_at": created_at,
            }
        )
        if capacity is not UNSET:
            field_dict["capacity"] = capacity
        if database_id is not UNSET:
            field_dict["database_id"] = database_id
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_connection_issue import DataConnectionIssue
        from ..models.data_pipeline_capacity import DataPipelineCapacity
        from ..models.data_source_table import DataSourceTable

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        connection_id = UUID(d.pop("connection_id"))

        connection_revision = d.pop("connection_revision")

        state = check_data_connection_check_state(d.pop("state"))

        fresh = d.pop("fresh")

        truncated = d.pop("truncated")

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = DataConnectionIssue.from_dict(checks_item_data)

            checks.append(checks_item)

        tables = []
        _tables = d.pop("tables")
        for tables_item_data in _tables:
            tables_item = DataSourceTable.from_dict(tables_item_data)

            tables.append(tables_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _capacity = d.pop("capacity", UNSET)
        capacity: DataPipelineCapacity | Unset
        if isinstance(_capacity, Unset):
            capacity = UNSET
        else:
            capacity = DataPipelineCapacity.from_dict(_capacity)

        _database_id = d.pop("database_id", UNSET)
        database_id: UUID | Unset
        if isinstance(_database_id, Unset):
            database_id = UNSET
        else:
            database_id = UUID(_database_id)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        data_connection_check = cls(
            id=id,
            connection_id=connection_id,
            connection_revision=connection_revision,
            state=state,
            fresh=fresh,
            truncated=truncated,
            checks=checks,
            tables=tables,
            created_at=created_at,
            capacity=capacity,
            database_id=database_id,
            observed_at=observed_at,
            expires_at=expires_at,
        )

        return data_connection_check
