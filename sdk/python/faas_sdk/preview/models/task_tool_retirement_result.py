from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="TaskToolRetirementResult")


@_attrs_define
class TaskToolRetirementResult:
    """Historical observation of the exact native park transition, drained instances and absent or expired candidate Task
    registrations. No active Task cancellation or current service-health certification is implied.

    """

    operation_id: str
    handover_id: str
    qualification_sha256: str
    worker_deployment_id: UUID
    serving_deployment_id: UUID
    park_transition_id: UUID
    worker_ids: list[UUID]
    evidence_sha256: str
    observed_at: datetime.datetime
    recorded_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        operation_id = self.operation_id

        handover_id = self.handover_id

        qualification_sha256 = self.qualification_sha256

        worker_deployment_id = str(self.worker_deployment_id)

        serving_deployment_id = str(self.serving_deployment_id)

        park_transition_id = str(self.park_transition_id)

        worker_ids = []
        for worker_ids_item_data in self.worker_ids:
            worker_ids_item = str(worker_ids_item_data)
            worker_ids.append(worker_ids_item)

        evidence_sha256 = self.evidence_sha256

        observed_at = self.observed_at.isoformat()

        recorded_at = self.recorded_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "handover_id": handover_id,
                "qualification_sha256": qualification_sha256,
                "worker_deployment_id": worker_deployment_id,
                "serving_deployment_id": serving_deployment_id,
                "park_transition_id": park_transition_id,
                "worker_ids": worker_ids,
                "evidence_sha256": evidence_sha256,
                "observed_at": observed_at,
                "recorded_at": recorded_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = d.pop("operation_id")

        handover_id = d.pop("handover_id")

        qualification_sha256 = d.pop("qualification_sha256")

        worker_deployment_id = UUID(d.pop("worker_deployment_id"))

        serving_deployment_id = UUID(d.pop("serving_deployment_id"))

        park_transition_id = UUID(d.pop("park_transition_id"))

        worker_ids = []
        _worker_ids = d.pop("worker_ids")
        for worker_ids_item_data in _worker_ids:
            worker_ids_item = UUID(worker_ids_item_data)

            worker_ids.append(worker_ids_item)

        evidence_sha256 = d.pop("evidence_sha256")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        task_tool_retirement_result = cls(
            operation_id=operation_id,
            handover_id=handover_id,
            qualification_sha256=qualification_sha256,
            worker_deployment_id=worker_deployment_id,
            serving_deployment_id=serving_deployment_id,
            park_transition_id=park_transition_id,
            worker_ids=worker_ids,
            evidence_sha256=evidence_sha256,
            observed_at=observed_at,
            recorded_at=recorded_at,
        )

        return task_tool_retirement_result
