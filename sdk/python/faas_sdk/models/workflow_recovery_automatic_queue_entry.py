from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_automatic_action import WorkflowRecoveryAutomaticAction


T = TypeVar("T", bound="WorkflowRecoveryAutomaticQueueEntry")


@_attrs_define
class WorkflowRecoveryAutomaticQueueEntry:
    """One pending or unresolved applied automatic retry. Queued_at is the frozen review preparation time. Ages are whole
    nonnegative seconds relative to the queue observed_at. Waiting age is omitted until the worker records a wait; the
    saved reason may be historical.

    """

    review_id: UUID
    policy_version: int
    queued_at: datetime.datetime
    queued_age_seconds: int
    action: WorkflowRecoveryAutomaticAction
    """Durable automatic retry intent and application receipt. Applied identifies child creation; its final result
    requires retained generation proof. Suppressed actions never run automatically."""
    waiting_age_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        review_id = str(self.review_id)

        policy_version = self.policy_version

        queued_at = self.queued_at.isoformat()

        queued_age_seconds = self.queued_age_seconds

        action = self.action.to_dict()

        waiting_age_seconds = self.waiting_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "review_id": review_id,
                "policy_version": policy_version,
                "queued_at": queued_at,
                "queued_age_seconds": queued_age_seconds,
                "action": action,
            }
        )
        if waiting_age_seconds is not UNSET:
            field_dict["waiting_age_seconds"] = waiting_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_automatic_action import WorkflowRecoveryAutomaticAction

        d = dict(src_dict)
        review_id = UUID(d.pop("review_id"))

        policy_version = d.pop("policy_version")

        queued_at = datetime.datetime.fromisoformat(d.pop("queued_at"))

        queued_age_seconds = d.pop("queued_age_seconds")

        action = WorkflowRecoveryAutomaticAction.from_dict(d.pop("action"))

        waiting_age_seconds = d.pop("waiting_age_seconds", UNSET)

        workflow_recovery_automatic_queue_entry = cls(
            review_id=review_id,
            policy_version=policy_version,
            queued_at=queued_at,
            queued_age_seconds=queued_age_seconds,
            action=action,
            waiting_age_seconds=waiting_age_seconds,
        )

        workflow_recovery_automatic_queue_entry.additional_properties = d
        return workflow_recovery_automatic_queue_entry

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
