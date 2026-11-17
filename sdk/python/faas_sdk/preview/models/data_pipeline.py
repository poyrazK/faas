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
    from ..models.data_pipeline_evolution import DataPipelineEvolution
    from ..models.data_pipeline_freshness import DataPipelineFreshness
    from ..models.data_pipeline_health import DataPipelineHealth
    from ..models.data_pipeline_index_advice import DataPipelineIndexAdvice
    from ..models.data_pipeline_operations import DataPipelineOperations
    from ..models.data_pipeline_operations_status import DataPipelineOperationsStatus
    from ..models.data_pipeline_schema_review import DataPipelineSchemaReview
    from ..models.data_pipeline_serving import DataPipelineServing
    from ..models.data_pipeline_tenant_change import DataPipelineTenantChange
    from ..models.data_pipeline_wal_retention import DataPipelineWALRetention


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
    operations: DataPipelineOperations | Unset = UNSET
    """Versioned policy for bounded scheduled snapshots, mirror quality checks and optional maintenance. Snapshot
    mode is chosen at creation and cannot be changed later."""
    operations_status: DataPipelineOperationsStatus | Unset = UNSET
    """Historical scheduled-work observations. Capture time bounds data age independently of the catalog worker
    observation timestamp."""
    schema_review: DataPipelineSchemaReview | Unset = UNSET
    """Worker-generated exact source-definition review with column and table metadata only. Its ID binds the
    generation, recorded baseline and requested source schema."""
    evolution: DataPipelineEvolution | Unset = UNSET
    """Frozen account and generation-bound serving preview. Approved indicates owner intent; applied requires
    worker verification and any required API cache refresh."""
    freshness_target_seconds: int | Unset = UNSET
    """Current owner-configured checkpoint confirmation target; zero means disabled and enabled targets range from
    15 to 86400 seconds."""
    freshness: DataPipelineFreshness | Unset = UNSET
    """Read-time confirmation of an applied checkpoint against the owner freshness target. Old confirmation is not
    measured replication lag; an idle source may already be caught up."""
    index_advice: DataPipelineIndexAdvice | Unset = UNSET
    """Frozen metadata-based mirror index advice. Size is a conservative estimate, not measured query performance.
    Explicit approval permits a transactional build for tables up to 64 MiB, indexes up to 32 MiB, at most eight
    owned indexes per pipeline, with a two-second deadline and 250 ms lock timeout. Failures roll back and require a
    fresh preview."""
    serving: DataPipelineServing | Unset = UNSET
    """Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1;
    approved evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no
    memberships. Column expansion requires source-definition approval, backfill, verification and API refresh."""
    tenant_change: DataPipelineTenantChange | Unset = UNSET
    """Frozen membership preview. Approval applies this exact after set atomically at the mirror before catalog
    acknowledgement."""
    wal_retention: DataPipelineWALRetention | Unset = UNSET
    """Fresh source-specific WAL headroom. The remaining-time estimate uses sampled cluster WAL generation and does
    not guarantee when a slot will be invalidated."""
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

        operations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.operations, Unset):
            operations = self.operations.to_dict()

        operations_status: dict[str, Any] | Unset = UNSET
        if not isinstance(self.operations_status, Unset):
            operations_status = self.operations_status.to_dict()

        schema_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.schema_review, Unset):
            schema_review = self.schema_review.to_dict()

        evolution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evolution, Unset):
            evolution = self.evolution.to_dict()

        freshness_target_seconds = self.freshness_target_seconds

        freshness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.freshness, Unset):
            freshness = self.freshness.to_dict()

        index_advice: dict[str, Any] | Unset = UNSET
        if not isinstance(self.index_advice, Unset):
            index_advice = self.index_advice.to_dict()

        serving: dict[str, Any] | Unset = UNSET
        if not isinstance(self.serving, Unset):
            serving = self.serving.to_dict()

        tenant_change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tenant_change, Unset):
            tenant_change = self.tenant_change.to_dict()

        wal_retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.wal_retention, Unset):
            wal_retention = self.wal_retention.to_dict()

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
        if operations is not UNSET:
            field_dict["operations"] = operations
        if operations_status is not UNSET:
            field_dict["operations_status"] = operations_status
        if schema_review is not UNSET:
            field_dict["schema_review"] = schema_review
        if evolution is not UNSET:
            field_dict["evolution"] = evolution
        if freshness_target_seconds is not UNSET:
            field_dict["freshness_target_seconds"] = freshness_target_seconds
        if freshness is not UNSET:
            field_dict["freshness"] = freshness
        if index_advice is not UNSET:
            field_dict["index_advice"] = index_advice
        if serving is not UNSET:
            field_dict["serving"] = serving
        if tenant_change is not UNSET:
            field_dict["tenant_change"] = tenant_change
        if wal_retention is not UNSET:
            field_dict["wal_retention"] = wal_retention
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
        from ..models.data_pipeline_evolution import DataPipelineEvolution
        from ..models.data_pipeline_freshness import DataPipelineFreshness
        from ..models.data_pipeline_health import DataPipelineHealth
        from ..models.data_pipeline_index_advice import DataPipelineIndexAdvice
        from ..models.data_pipeline_operations import DataPipelineOperations
        from ..models.data_pipeline_operations_status import DataPipelineOperationsStatus
        from ..models.data_pipeline_schema_review import DataPipelineSchemaReview
        from ..models.data_pipeline_serving import DataPipelineServing
        from ..models.data_pipeline_tenant_change import DataPipelineTenantChange
        from ..models.data_pipeline_wal_retention import DataPipelineWALRetention

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

        _operations = d.pop("operations", UNSET)
        operations: DataPipelineOperations | Unset
        if isinstance(_operations, Unset):
            operations = UNSET
        else:
            operations = DataPipelineOperations.from_dict(_operations)

        _operations_status = d.pop("operations_status", UNSET)
        operations_status: DataPipelineOperationsStatus | Unset
        if isinstance(_operations_status, Unset):
            operations_status = UNSET
        else:
            operations_status = DataPipelineOperationsStatus.from_dict(_operations_status)

        _schema_review = d.pop("schema_review", UNSET)
        schema_review: DataPipelineSchemaReview | Unset
        if isinstance(_schema_review, Unset):
            schema_review = UNSET
        else:
            schema_review = DataPipelineSchemaReview.from_dict(_schema_review)

        _evolution = d.pop("evolution", UNSET)
        evolution: DataPipelineEvolution | Unset
        if isinstance(_evolution, Unset):
            evolution = UNSET
        else:
            evolution = DataPipelineEvolution.from_dict(_evolution)

        freshness_target_seconds = d.pop("freshness_target_seconds", UNSET)

        _freshness = d.pop("freshness", UNSET)
        freshness: DataPipelineFreshness | Unset
        if isinstance(_freshness, Unset):
            freshness = UNSET
        else:
            freshness = DataPipelineFreshness.from_dict(_freshness)

        _index_advice = d.pop("index_advice", UNSET)
        index_advice: DataPipelineIndexAdvice | Unset
        if isinstance(_index_advice, Unset):
            index_advice = UNSET
        else:
            index_advice = DataPipelineIndexAdvice.from_dict(_index_advice)

        _serving = d.pop("serving", UNSET)
        serving: DataPipelineServing | Unset
        if isinstance(_serving, Unset):
            serving = UNSET
        else:
            serving = DataPipelineServing.from_dict(_serving)

        _tenant_change = d.pop("tenant_change", UNSET)
        tenant_change: DataPipelineTenantChange | Unset
        if isinstance(_tenant_change, Unset):
            tenant_change = UNSET
        else:
            tenant_change = DataPipelineTenantChange.from_dict(_tenant_change)

        _wal_retention = d.pop("wal_retention", UNSET)
        wal_retention: DataPipelineWALRetention | Unset
        if isinstance(_wal_retention, Unset):
            wal_retention = UNSET
        else:
            wal_retention = DataPipelineWALRetention.from_dict(_wal_retention)

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
            operations=operations,
            operations_status=operations_status,
            schema_review=schema_review,
            evolution=evolution,
            freshness_target_seconds=freshness_target_seconds,
            freshness=freshness,
            index_advice=index_advice,
            serving=serving,
            tenant_change=tenant_change,
            wal_retention=wal_retention,
            applied_through_at=applied_through_at,
            health=health,
            capacity=capacity,
            reason=reason,
            retained_wal_bytes=retained_wal_bytes,
            observed_at=observed_at,
            verified_at=verified_at,
        )

        return data_pipeline
