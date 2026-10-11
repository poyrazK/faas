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
    from ..models.operation_delivery_response import OperationDeliveryResponse
    from ..models.operation_progress import OperationProgress
    from ..models.operation_result_artifact import OperationResultArtifact
    from ..models.operation_subject import OperationSubject


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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_delivery_response import OperationDeliveryResponse
        from ..models.operation_progress import OperationProgress
        from ..models.operation_result_artifact import OperationResultArtifact
        from ..models.operation_subject import OperationSubject

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
