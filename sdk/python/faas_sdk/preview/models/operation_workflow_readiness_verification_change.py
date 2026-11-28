from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_verification_change_before_status import (
    OperationWorkflowReadinessVerificationChangeBeforeStatus,
    check_operation_workflow_readiness_verification_change_before_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_verification_watch_item import (
        OperationWorkflowReadinessVerificationWatchItem,
    )


T = TypeVar("T", bound="OperationWorkflowReadinessVerificationChange")


@_attrs_define
class OperationWorkflowReadinessVerificationChange:
    """A changed disposition of one captured resolution, delivered once for an observed status change."""

    before_status: OperationWorkflowReadinessVerificationChangeBeforeStatus
    """OperationWorkflowReadinessVerificationChange.before_status. Server-managed advisory verification observation
    for the retained source completion."""
    after: OperationWorkflowReadinessVerificationWatchItem
    """Saved resolution identity and latest watch disposition. Missing proof is unavailable; invalidated means an
    explicit later application report reopened the blocker. Proof is the last retained observation, not current
    readiness."""

    def to_dict(self) -> dict[str, Any]:
        before_status: str = self.before_status

        after = self.after.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "before_status": before_status,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_verification_watch_item import (
            OperationWorkflowReadinessVerificationWatchItem,
        )

        d = dict(src_dict)
        before_status = check_operation_workflow_readiness_verification_change_before_status(d.pop("before_status"))

        after = OperationWorkflowReadinessVerificationWatchItem.from_dict(d.pop("after"))

        operation_workflow_readiness_verification_change = cls(
            before_status=before_status,
            after=after,
        )

        return operation_workflow_readiness_verification_change
