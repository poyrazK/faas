from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.issue_lifecycle_alert_preview_event import (
    IssueLifecycleAlertPreviewEvent,
    check_issue_lifecycle_alert_preview_event,
)
from ..models.issue_lifecycle_alert_preview_reason import (
    IssueLifecycleAlertPreviewReason,
    check_issue_lifecycle_alert_preview_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueLifecycleAlertPreview")


@_attrs_define
class IssueLifecycleAlertPreview:
    """Policy-only eligibility; actual Slack delivery has additional subscription, routing and sender gates."""

    issue_id: UUID
    event: IssueLifecycleAlertPreviewEvent
    environment: str
    policy_revision: int
    allowed: bool
    reason: IssueLifecycleAlertPreviewReason
    next_eligible_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        issue_id = str(self.issue_id)

        event: str = self.event

        environment = self.environment

        policy_revision = self.policy_revision

        allowed = self.allowed

        reason: str = self.reason

        next_eligible_at: str | Unset = UNSET
        if not isinstance(self.next_eligible_at, Unset):
            next_eligible_at = self.next_eligible_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issue_id": issue_id,
                "event": event,
                "environment": environment,
                "policy_revision": policy_revision,
                "allowed": allowed,
                "reason": reason,
            }
        )
        if next_eligible_at is not UNSET:
            field_dict["next_eligible_at"] = next_eligible_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        issue_id = UUID(d.pop("issue_id"))

        event = check_issue_lifecycle_alert_preview_event(d.pop("event"))

        environment = d.pop("environment")

        policy_revision = d.pop("policy_revision")

        allowed = d.pop("allowed")

        reason = check_issue_lifecycle_alert_preview_reason(d.pop("reason"))

        _next_eligible_at = d.pop("next_eligible_at", UNSET)
        next_eligible_at: datetime.datetime | Unset
        if isinstance(_next_eligible_at, Unset):
            next_eligible_at = UNSET
        else:
            next_eligible_at = datetime.datetime.fromisoformat(_next_eligible_at)

        issue_lifecycle_alert_preview = cls(
            issue_id=issue_id,
            event=event,
            environment=environment,
            policy_revision=policy_revision,
            allowed=allowed,
            reason=reason,
            next_eligible_at=next_eligible_at,
        )

        return issue_lifecycle_alert_preview
