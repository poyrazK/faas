from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.data_pipeline_desired_state import DataPipelineDesiredState, check_data_pipeline_desired_state
from ..models.data_pipeline_state import DataPipelineState, check_data_pipeline_state
from ..models.data_pipeline_verification import DataPipelineVerification, check_data_pipeline_verification
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipeline")


@_attrs_define
class DataPipeline:
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
            reason=reason,
            retained_wal_bytes=retained_wal_bytes,
            observed_at=observed_at,
            verified_at=verified_at,
        )

        return data_pipeline
