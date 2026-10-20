from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_response_state import OperationResponseState, check_operation_response_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_data_repair import OperationDataRepair
    from ..models.operation_delivery_response import OperationDeliveryResponse
    from ..models.operation_limits import OperationLimits
    from ..models.operation_progress import OperationProgress
    from ..models.operation_result_artifact import OperationResultArtifact
    from ..models.operation_subject import OperationSubject
    from ..models.operation_usage import OperationUsage
    from ..models.operation_waiting_state import OperationWaitingState


T = TypeVar("T", bound="OperationResponse")


@_attrs_define
class OperationResponse:
    """Customer business work; execution recovery and delivery retain independent semantics."""

    id: UUID
    name: str
    generation: int
    state: OperationResponseState
    completion_delivery: OperationDeliveryResponse
    """Independent completion notification projection from the webhook outbox."""
    cancellation_requested: bool
    latest_sequence: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    expires_at: datetime.datetime
    subject: OperationSubject | Unset = UNSET
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    progress: OperationProgress | Unset = UNSET
    """Current bounded progress from the active execution attempt."""
    result: Any | Unset = UNSET
    """Pinned-schema-validated business result, when confirmed successful."""
    artifacts: list[OperationResultArtifact] | Unset = UNSET
    failure_code: str | Unset = UNSET
    """Stable failure reason. deadline_exceeded and budget_exhausted mean the operation's own limits settled it
    (ADR-1196)."""
    waiting: OperationWaitingState | Unset = UNSET
    """Why a waiting operation holds no execution. until is the timer's wake time or the approval deadline."""
    data_repair: OperationDataRepair | Unset = UNSET
    """Repair plan metadata in Operation history; captured row values remain in the application executor."""
    limits: OperationLimits | Unset = UNSET
    """ADR-1196 bounds fixed onto each accepted Operation. Every field is optional and bounded by the plan;
    workflow definitions take a deadline and an execution budget (ADR-20261016110803), and job definitions take none."""
    deadline_at: datetime.datetime | Unset = UNSET
    """Absolute deadline fixed at acceptance; waits, retries and recovery count against it."""
    usage: OperationUsage | Unset = UNSET
    """What an Operation with limits has consumed. A running attempt is added when it ends."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        generation = self.generation

        state: str = self.state

        completion_delivery = self.completion_delivery.to_dict()

        cancellation_requested = self.cancellation_requested

        latest_sequence = self.latest_sequence

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        expires_at = self.expires_at.isoformat()

        subject: dict[str, Any] | Unset = UNSET
        if not isinstance(self.subject, Unset):
            subject = self.subject.to_dict()

        progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progress, Unset):
            progress = self.progress.to_dict()

        result = self.result

        artifacts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.artifacts, Unset):
            artifacts = []
            for artifacts_item_data in self.artifacts:
                artifacts_item = artifacts_item_data.to_dict()
                artifacts.append(artifacts_item)

        failure_code = self.failure_code

        waiting: dict[str, Any] | Unset = UNSET
        if not isinstance(self.waiting, Unset):
            waiting = self.waiting.to_dict()

        data_repair: dict[str, Any] | Unset = UNSET
        if not isinstance(self.data_repair, Unset):
            data_repair = self.data_repair.to_dict()

        limits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.limits, Unset):
            limits = self.limits.to_dict()

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        usage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.usage, Unset):
            usage = self.usage.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "generation": generation,
                "state": state,
                "completion_delivery": completion_delivery,
                "cancellation_requested": cancellation_requested,
                "latest_sequence": latest_sequence,
                "created_at": created_at,
                "updated_at": updated_at,
                "expires_at": expires_at,
            }
        )
        if subject is not UNSET:
            field_dict["subject"] = subject
        if progress is not UNSET:
            field_dict["progress"] = progress
        if result is not UNSET:
            field_dict["result"] = result
        if artifacts is not UNSET:
            field_dict["artifacts"] = artifacts
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if waiting is not UNSET:
            field_dict["waiting"] = waiting
        if data_repair is not UNSET:
            field_dict["data_repair"] = data_repair
        if limits is not UNSET:
            field_dict["limits"] = limits
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if usage is not UNSET:
            field_dict["usage"] = usage

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_data_repair import OperationDataRepair
        from ..models.operation_delivery_response import OperationDeliveryResponse
        from ..models.operation_limits import OperationLimits
        from ..models.operation_progress import OperationProgress
        from ..models.operation_result_artifact import OperationResultArtifact
        from ..models.operation_subject import OperationSubject
        from ..models.operation_usage import OperationUsage
        from ..models.operation_waiting_state import OperationWaitingState

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        generation = d.pop("generation")

        state = check_operation_response_state(d.pop("state"))

        completion_delivery = OperationDeliveryResponse.from_dict(d.pop("completion_delivery"))

        cancellation_requested = d.pop("cancellation_requested")

        latest_sequence = d.pop("latest_sequence")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _subject = d.pop("subject", UNSET)
        subject: OperationSubject | Unset
        if isinstance(_subject, Unset):
            subject = UNSET
        else:
            subject = OperationSubject.from_dict(_subject)

        _progress = d.pop("progress", UNSET)
        progress: OperationProgress | Unset
        if isinstance(_progress, Unset):
            progress = UNSET
        else:
            progress = OperationProgress.from_dict(_progress)

        result = d.pop("result", UNSET)

        _artifacts = d.pop("artifacts", UNSET)
        artifacts: list[OperationResultArtifact] | Unset = UNSET
        if _artifacts is not UNSET:
            artifacts = []
            for artifacts_item_data in _artifacts:
                artifacts_item = OperationResultArtifact.from_dict(artifacts_item_data)

                artifacts.append(artifacts_item)

        failure_code = d.pop("failure_code", UNSET)

        _waiting = d.pop("waiting", UNSET)
        waiting: OperationWaitingState | Unset
        if isinstance(_waiting, Unset):
            waiting = UNSET
        else:
            waiting = OperationWaitingState.from_dict(_waiting)

        _data_repair = d.pop("data_repair", UNSET)
        data_repair: OperationDataRepair | Unset
        if isinstance(_data_repair, Unset):
            data_repair = UNSET
        else:
            data_repair = OperationDataRepair.from_dict(_data_repair)

        _limits = d.pop("limits", UNSET)
        limits: OperationLimits | Unset
        if isinstance(_limits, Unset):
            limits = UNSET
        else:
            limits = OperationLimits.from_dict(_limits)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        _usage = d.pop("usage", UNSET)
        usage: OperationUsage | Unset
        if isinstance(_usage, Unset):
            usage = UNSET
        else:
            usage = OperationUsage.from_dict(_usage)

        operation_response = cls(
            id=id,
            name=name,
            generation=generation,
            state=state,
            completion_delivery=completion_delivery,
            cancellation_requested=cancellation_requested,
            latest_sequence=latest_sequence,
            created_at=created_at,
            updated_at=updated_at,
            expires_at=expires_at,
            subject=subject,
            progress=progress,
            result=result,
            artifacts=artifacts,
            failure_code=failure_code,
            waiting=waiting,
            data_repair=data_repair,
            limits=limits,
            deadline_at=deadline_at,
            usage=usage,
        )

        operation_response.additional_properties = d
        return operation_response

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
