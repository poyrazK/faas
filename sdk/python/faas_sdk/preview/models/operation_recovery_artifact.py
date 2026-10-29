from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_recovery_artifact_state import (
    OperationRecoveryArtifactState,
    check_operation_recovery_artifact_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationRecoveryArtifact")


@_attrs_define
class OperationRecoveryArtifact:
    """Metadata for a prepared private copy or attached result reference, without download authority."""

    id: UUID
    name: str
    generation: int
    attempt: int
    state: OperationRecoveryArtifactState
    retained: bool
    """An owned retained storage receipt matches this reference at observation time; bytes are not fetched by
    inspection."""
    size_bytes: int
    sha256: str
    expires_at: datetime.datetime
    workflow_step: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        generation = self.generation

        attempt = self.attempt

        state: str = self.state

        retained = self.retained

        size_bytes = self.size_bytes

        sha256 = self.sha256

        expires_at = self.expires_at.isoformat()

        workflow_step = self.workflow_step

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "generation": generation,
                "attempt": attempt,
                "state": state,
                "retained": retained,
                "size_bytes": size_bytes,
                "sha256": sha256,
                "expires_at": expires_at,
            }
        )
        if workflow_step is not UNSET:
            field_dict["workflow_step"] = workflow_step

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        generation = d.pop("generation")

        attempt = d.pop("attempt")

        state = check_operation_recovery_artifact_state(d.pop("state"))

        retained = d.pop("retained")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        workflow_step = d.pop("workflow_step", UNSET)

        operation_recovery_artifact = cls(
            id=id,
            name=name,
            generation=generation,
            attempt=attempt,
            state=state,
            retained=retained,
            size_bytes=size_bytes,
            sha256=sha256,
            expires_at=expires_at,
            workflow_step=workflow_step,
        )

        return operation_recovery_artifact
