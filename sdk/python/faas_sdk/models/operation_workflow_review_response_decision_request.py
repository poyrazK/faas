from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_decision_request_kind import (
    OperationWorkflowReviewResponseDecisionRequestKind,
    check_operation_workflow_review_response_decision_request_kind,
)

T = TypeVar("T", bound="OperationWorkflowReviewResponseDecisionRequest")


@_attrs_define
class OperationWorkflowReviewResponseDecisionRequest:
    """Explicit advisory continue, revise or pause decision on an existing active response. Saving re-observes scoped
    sources and checks the reviewed health digest again under the notification lock.

    """

    kind: OperationWorkflowReviewResponseDecisionRequestKind
    """OperationWorkflowReviewResponseDecisionRequest.kind. Human intent; pause keeps check-ins active and does not
    stop application execution."""
    reason: str
    """OperationWorkflowReviewResponseDecisionRequest.reason. Nonblank UTF-8 explanation up to 1024 bytes; line
    breaks and tabs allowed."""
    health_fingerprint: str
    """OperationWorkflowReviewResponseDecisionRequest.health_fingerprint. Digest from the sampled health the
    operator reviewed; absent, legacy or changed observations conflict."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        reason = self.reason

        health_fingerprint = self.health_fingerprint

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "reason": reason,
                "health_fingerprint": health_fingerprint,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_operation_workflow_review_response_decision_request_kind(d.pop("kind"))

        reason = d.pop("reason")

        health_fingerprint = d.pop("health_fingerprint")

        operation_workflow_review_response_decision_request = cls(
            kind=kind,
            reason=reason,
            health_fingerprint=health_fingerprint,
        )

        return operation_workflow_review_response_decision_request
