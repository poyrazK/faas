from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_completed_attempt_status import (
    ExecutionPipelineCompletedAttemptStatus,
    check_execution_pipeline_completed_attempt_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_artifact_ref import ExecutionPipelineArtifactRef
    from ..models.execution_usage import ExecutionUsage


T = TypeVar("T", bound="ExecutionPipelineCompletedAttempt")


@_attrs_define
class ExecutionPipelineCompletedAttempt:
    """Recorded final status and timing for one attempt in a completed pipeline."""

    run_id: UUID
    attempt: int
    status: ExecutionPipelineCompletedAttemptStatus
    receipt: str
    usage: ExecutionUsage
    """Host-measured resource usage for a terminal execution."""
    artifacts: list[ExecutionPipelineArtifactRef]
    failure_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        attempt = self.attempt

        status: str = self.status

        receipt = self.receipt

        usage = self.usage.to_dict()

        artifacts = []
        for artifacts_item_data in self.artifacts:
            artifacts_item = artifacts_item_data.to_dict()
            artifacts.append(artifacts_item)

        failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "attempt": attempt,
                "status": status,
                "receipt": receipt,
                "usage": usage,
                "artifacts": artifacts,
            }
        )
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_artifact_ref import ExecutionPipelineArtifactRef
        from ..models.execution_usage import ExecutionUsage

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        attempt = d.pop("attempt")

        status = check_execution_pipeline_completed_attempt_status(d.pop("status"))

        receipt = d.pop("receipt")

        usage = ExecutionUsage.from_dict(d.pop("usage"))

        artifacts = []
        _artifacts = d.pop("artifacts")
        for artifacts_item_data in _artifacts:
            artifacts_item = ExecutionPipelineArtifactRef.from_dict(artifacts_item_data)

            artifacts.append(artifacts_item)

        failure_code = d.pop("failure_code", UNSET)

        execution_pipeline_completed_attempt = cls(
            run_id=run_id,
            attempt=attempt,
            status=status,
            receipt=receipt,
            usage=usage,
            artifacts=artifacts,
            failure_code=failure_code,
        )

        execution_pipeline_completed_attempt.additional_properties = d
        return execution_pipeline_completed_attempt

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
