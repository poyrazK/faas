from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_review_status_outcome import (
    WorkflowRecoveryReviewStatusOutcome,
    check_workflow_recovery_review_status_outcome,
)

if TYPE_CHECKING:
    from ..models.workflow_recovery_review_action_status import WorkflowRecoveryReviewActionStatus


T = TypeVar("T", bound="WorkflowRecoveryReviewStatus")


@_attrs_define
class WorkflowRecoveryReviewStatus:
    """Application status and aggregate execution outcome. Complete requires final proof for every action; retained
    completion snapshots take precedence over later generations and pruned execution rows.

    """

    review_id: UUID
    observed_at: datetime.datetime
    status: str
    outcome: WorkflowRecoveryReviewStatusOutcome
    complete: bool
    actions: list[WorkflowRecoveryReviewActionStatus]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        review_id = str(self.review_id)

        observed_at = self.observed_at.isoformat()

        status = self.status

        outcome: str = self.outcome

        complete = self.complete

        actions = []
        for actions_item_data in self.actions:
            actions_item = actions_item_data.to_dict()
            actions.append(actions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "review_id": review_id,
                "observed_at": observed_at,
                "status": status,
                "outcome": outcome,
                "complete": complete,
                "actions": actions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_review_action_status import WorkflowRecoveryReviewActionStatus

        d = dict(src_dict)
        review_id = UUID(d.pop("review_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        status = d.pop("status")

        outcome = check_workflow_recovery_review_status_outcome(d.pop("outcome"))

        complete = d.pop("complete")

        actions = []
        _actions = d.pop("actions")
        for actions_item_data in _actions:
            actions_item = WorkflowRecoveryReviewActionStatus.from_dict(actions_item_data)

            actions.append(actions_item)

        workflow_recovery_review_status = cls(
            review_id=review_id,
            observed_at=observed_at,
            status=status,
            outcome=outcome,
            complete=complete,
            actions=actions,
        )

        workflow_recovery_review_status.additional_properties = d
        return workflow_recovery_review_status

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
