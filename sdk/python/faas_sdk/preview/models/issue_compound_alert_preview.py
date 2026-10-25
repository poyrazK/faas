from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.issue_compound_alert_preview_reason import (
    IssueCompoundAlertPreviewReason,
    check_issue_compound_alert_preview_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueCompoundAlertPreview")


@_attrs_define
class IssueCompoundAlertPreview:
    """Predicate results and retained shared-window counts from a read-only compound-rule evaluation."""

    issue_id: UUID
    rule_key: str
    environment: str
    policy_revision: int
    window_start: datetime.datetime
    window_end: datetime.datetime
    observed_events: int
    identified_customers: int
    enabled: bool
    environment_matches: bool
    events_matched: bool
    customers_matched: bool
    suppressed: bool
    cooldown_ready: bool
    allowed: bool
    reason: IssueCompoundAlertPreviewReason
    next_eligible_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        issue_id = str(self.issue_id)

        rule_key = self.rule_key

        environment = self.environment

        policy_revision = self.policy_revision

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        observed_events = self.observed_events

        identified_customers = self.identified_customers

        enabled = self.enabled

        environment_matches = self.environment_matches

        events_matched = self.events_matched

        customers_matched = self.customers_matched

        suppressed = self.suppressed

        cooldown_ready = self.cooldown_ready

        allowed = self.allowed

        reason: str = self.reason

        next_eligible_at: str | Unset = UNSET
        if not isinstance(self.next_eligible_at, Unset):
            next_eligible_at = self.next_eligible_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issue_id": issue_id,
                "rule_key": rule_key,
                "environment": environment,
                "policy_revision": policy_revision,
                "window_start": window_start,
                "window_end": window_end,
                "observed_events": observed_events,
                "identified_customers": identified_customers,
                "enabled": enabled,
                "environment_matches": environment_matches,
                "events_matched": events_matched,
                "customers_matched": customers_matched,
                "suppressed": suppressed,
                "cooldown_ready": cooldown_ready,
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

        rule_key = d.pop("rule_key")

        environment = d.pop("environment")

        policy_revision = d.pop("policy_revision")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        observed_events = d.pop("observed_events")

        identified_customers = d.pop("identified_customers")

        enabled = d.pop("enabled")

        environment_matches = d.pop("environment_matches")

        events_matched = d.pop("events_matched")

        customers_matched = d.pop("customers_matched")

        suppressed = d.pop("suppressed")

        cooldown_ready = d.pop("cooldown_ready")

        allowed = d.pop("allowed")

        reason = check_issue_compound_alert_preview_reason(d.pop("reason"))

        _next_eligible_at = d.pop("next_eligible_at", UNSET)
        next_eligible_at: datetime.datetime | Unset
        if isinstance(_next_eligible_at, Unset):
            next_eligible_at = UNSET
        else:
            next_eligible_at = datetime.datetime.fromisoformat(_next_eligible_at)

        issue_compound_alert_preview = cls(
            issue_id=issue_id,
            rule_key=rule_key,
            environment=environment,
            policy_revision=policy_revision,
            window_start=window_start,
            window_end=window_end,
            observed_events=observed_events,
            identified_customers=identified_customers,
            enabled=enabled,
            environment_matches=environment_matches,
            events_matched=events_matched,
            customers_matched=customers_matched,
            suppressed=suppressed,
            cooldown_ready=cooldown_ready,
            allowed=allowed,
            reason=reason,
            next_eligible_at=next_eligible_at,
        )

        return issue_compound_alert_preview
