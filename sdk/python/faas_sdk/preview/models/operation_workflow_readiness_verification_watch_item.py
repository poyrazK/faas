from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_verification_watch_item_status import (
    OperationWorkflowReadinessVerificationWatchItemStatus,
    check_operation_workflow_readiness_verification_watch_item_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification


T = TypeVar("T", bound="OperationWorkflowReadinessVerificationWatchItem")


@_attrs_define
class OperationWorkflowReadinessVerificationWatchItem:
    """Saved resolution identity and latest watch disposition. Missing proof is unavailable; invalidated means an explicit
    later application report reopened the blocker. Proof is the last retained observation, not current readiness.

    """

    blocker_active: bool
    """OperationWorkflowReadinessVerificationWatchItem.blocker_active. Whether the blocker was present at the last
    comparable observation; an existing active blocker is not a new reopening."""
    key: str
    """OperationWorkflowReadinessVerificationWatchItem.key. Server-managed advisory verification observation for
    the retained source completion."""
    status: OperationWorkflowReadinessVerificationWatchItemStatus
    """OperationWorkflowReadinessVerificationWatchItem.status. Server-managed advisory verification observation for
    the retained source completion."""
    explanation: str
    """OperationWorkflowReadinessVerificationWatchItem.explanation. Server-managed advisory verification
    observation for the retained source completion."""
    proof: OperationWorkflowResolutionVerification
    """Retained resolution claim and its exact proof-verification status under the original scoped workflow
    contract."""
    reporting_revision: int | Unset = UNSET
    """OperationWorkflowReadinessVerificationWatchItem.reporting_revision. Exact state-report revision observed for
    the reporting or invalidating Operation."""
    reporting_operation_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessVerificationWatchItem.reporting_operation_id. Server-managed advisory verification
    observation for the retained source completion."""

    def to_dict(self) -> dict[str, Any]:
        blocker_active = self.blocker_active

        key = self.key

        status: str = self.status

        explanation = self.explanation

        proof = self.proof.to_dict()

        reporting_revision = self.reporting_revision

        reporting_operation_id: str | Unset = UNSET
        if not isinstance(self.reporting_operation_id, Unset):
            reporting_operation_id = str(self.reporting_operation_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "blocker_active": blocker_active,
                "key": key,
                "status": status,
                "explanation": explanation,
                "proof": proof,
            }
        )
        if reporting_revision is not UNSET:
            field_dict["reporting_revision"] = reporting_revision
        if reporting_operation_id is not UNSET:
            field_dict["reporting_operation_id"] = reporting_operation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification

        d = dict(src_dict)
        blocker_active = d.pop("blocker_active")

        key = d.pop("key")

        status = check_operation_workflow_readiness_verification_watch_item_status(d.pop("status"))

        explanation = d.pop("explanation")

        proof = OperationWorkflowResolutionVerification.from_dict(d.pop("proof"))

        reporting_revision = d.pop("reporting_revision", UNSET)

        _reporting_operation_id = d.pop("reporting_operation_id", UNSET)
        reporting_operation_id: UUID | Unset
        if isinstance(_reporting_operation_id, Unset):
            reporting_operation_id = UNSET
        else:
            reporting_operation_id = UUID(_reporting_operation_id)

        operation_workflow_readiness_verification_watch_item = cls(
            blocker_active=blocker_active,
            key=key,
            status=status,
            explanation=explanation,
            proof=proof,
            reporting_revision=reporting_revision,
            reporting_operation_id=reporting_operation_id,
        )

        return operation_workflow_readiness_verification_watch_item
