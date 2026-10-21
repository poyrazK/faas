from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.data_pipeline_desired_state import DataPipelineDesiredState, check_data_pipeline_desired_state
from ..models.data_pipeline_state import DataPipelineState, check_data_pipeline_state
from ..models.data_pipeline_verification import DataPipelineVerification, check_data_pipeline_verification
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_capacity import DataPipelineCapacity
    from ..models.data_pipeline_health import DataPipelineHealth


T = TypeVar("T", bound="DataPipeline")


@_attrs_define
class DataPipeline:
    """Managed replication intent with bounded, revision-specific worker observations."""

    observed_revision: int
    """Intent revision covered by the latest observation."""
    observation_fresh: bool
    """True only when the current intent was observed within the last 90 seconds."""
    id: UUID
    name: str
    source_connection_id: UUID
    database_id: UUID
    tables: list[str]
    read_subjects: list[str]
    desired_state: DataPipelineDesiredState
    state: DataPipelineState
    revision: int
    generation: int
    tables_ready: int
    verification: DataPipelineVerification
    created_at: datetime.datetime
    applied_through_at: datetime.datetime | Unset = UNSET
    """Conservative source checkpoint time confirmed applied by the destination replication origin. Historical
    evidence, not measured time lag."""
    health: DataPipelineHealth | Unset = UNSET
    """Read-time freshness assessment and safe recovery guidance. Unconfirmed checkpoints can occur on idle
    sources."""
    capacity: DataPipelineCapacity | Unset = UNSET
    """New-allocation readiness at observation time. Admission atomically rechecks and reserves capacity;
    observations alone reserve nothing."""
    reason: str | Unset = UNSET
    retained_wal_bytes: int | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    verified_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        observed_revision = self.observed_revision

        observation_fresh = self.observation_fresh

        id = str(self.id)

        name = self.name

        source_connection_id = str(self.source_connection_id)

        database_id = str(self.database_id)

        tables = self.tables

        read_subjects = self.read_subjects

        desired_state: str = self.desired_state

        state: str = self.state

        revision = self.revision

        generation = self.generation

        tables_ready = self.tables_ready

        verification: str = self.verification

        created_at = self.created_at.isoformat()

        applied_through_at: str | Unset = UNSET
        if not isinstance(self.applied_through_at, Unset):
            applied_through_at = self.applied_through_at.isoformat()

        health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.health, Unset):
            health = self.health.to_dict()

        capacity: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity, Unset):
            capacity = self.capacity.to_dict()

        reason = self.reason

        retained_wal_bytes = self.retained_wal_bytes

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_revision": observed_revision,
                "observation_fresh": observation_fresh,
                "id": id,
                "name": name,
                "source_connection_id": source_connection_id,
                "database_id": database_id,
                "tables": tables,
                "read_subjects": read_subjects,
                "desired_state": desired_state,
                "state": state,
                "revision": revision,
                "generation": generation,
                "tables_ready": tables_ready,
                "verification": verification,
                "created_at": created_at,
            }
        )
        if applied_through_at is not UNSET:
            field_dict["applied_through_at"] = applied_through_at
        if health is not UNSET:
            field_dict["health"] = health
        if capacity is not UNSET:
            field_dict["capacity"] = capacity
        if reason is not UNSET:
            field_dict["reason"] = reason
        if retained_wal_bytes is not UNSET:
            field_dict["retained_wal_bytes"] = retained_wal_bytes
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_capacity import DataPipelineCapacity
        from ..models.data_pipeline_health import DataPipelineHealth

        d = dict(src_dict)
        observed_revision = d.pop("observed_revision")

        observation_fresh = d.pop("observation_fresh")

        id = UUID(d.pop("id"))

        name = d.pop("name")

        source_connection_id = UUID(d.pop("source_connection_id"))

        database_id = UUID(d.pop("database_id"))

        tables = cast(list[str], d.pop("tables"))

        read_subjects = cast(list[str], d.pop("read_subjects"))

        desired_state = check_data_pipeline_desired_state(d.pop("desired_state"))

        state = check_data_pipeline_state(d.pop("state"))

        revision = d.pop("revision")

        generation = d.pop("generation")

        tables_ready = d.pop("tables_ready")

        verification = check_data_pipeline_verification(d.pop("verification"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _applied_through_at = d.pop("applied_through_at", UNSET)
        applied_through_at: datetime.datetime | Unset
        if isinstance(_applied_through_at, Unset):
            applied_through_at = UNSET
        else:
            applied_through_at = datetime.datetime.fromisoformat(_applied_through_at)

        _health = d.pop("health", UNSET)
        health: DataPipelineHealth | Unset
        if isinstance(_health, Unset):
            health = UNSET
        else:
            health = DataPipelineHealth.from_dict(_health)

        _capacity = d.pop("capacity", UNSET)
        capacity: DataPipelineCapacity | Unset
        if isinstance(_capacity, Unset):
            capacity = UNSET
        else:
            capacity = DataPipelineCapacity.from_dict(_capacity)

        reason = d.pop("reason", UNSET)

        retained_wal_bytes = d.pop("retained_wal_bytes", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        data_pipeline = cls(
            observed_revision=observed_revision,
            observation_fresh=observation_fresh,
            id=id,
            name=name,
            source_connection_id=source_connection_id,
            database_id=database_id,
            tables=tables,
            read_subjects=read_subjects,
            desired_state=desired_state,
            state=state,
            revision=revision,
            generation=generation,
            tables_ready=tables_ready,
            verification=verification,
            created_at=created_at,
            applied_through_at=applied_through_at,
            health=health,
            capacity=capacity,
            reason=reason,
            retained_wal_bytes=retained_wal_bytes,
            observed_at=observed_at,
            verified_at=verified_at,
        )

        return data_pipeline
