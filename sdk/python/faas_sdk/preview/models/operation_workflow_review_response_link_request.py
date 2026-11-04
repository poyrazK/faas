from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseLinkRequest")


@_attrs_define
class OperationWorkflowReviewResponseLinkRequest:
    """OperationWorkflowReviewResponseLinkRequest. Reference to a retained dependency review delivery in the same
    application and environment.

    """

    subscription_id: UUID
    """OperationWorkflowReviewResponseLinkRequest.subscription_id. Source review subscription identity."""
    sequence: int
    """OperationWorkflowReviewResponseLinkRequest.sequence. Source review notification sequence."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "sequence": sequence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        operation_workflow_review_response_link_request = cls(
            subscription_id=subscription_id,
            sequence=sequence,
        )

        return operation_workflow_review_response_link_request
