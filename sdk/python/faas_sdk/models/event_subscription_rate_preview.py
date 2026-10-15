from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_subscription_rate_preview_capacity_blocking_scopes_item import (
    EventSubscriptionRatePreviewCapacityBlockingScopesItem,
    check_event_subscription_rate_preview_capacity_blocking_scopes_item,
)
from ..models.event_subscription_rate_preview_coverage import (
    EventSubscriptionRatePreviewCoverage,
    check_event_subscription_rate_preview_coverage,
)
from ..models.event_subscription_rate_preview_sample_limit import (
    EventSubscriptionRatePreviewSampleLimit,
    check_event_subscription_rate_preview_sample_limit,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_rate_deadline_risk import EventSubscriptionRateDeadlineRisk
    from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit


T = TypeVar("T", bound="EventSubscriptionRatePreview")


@_attrs_define
class EventSubscriptionRatePreview:
    """One snapshot of current controls and a hypothetical rate-only admission model. The deterministic oldest-first model
    is not an ordering guarantee, completion ETA or deadline-safety certificate. Full pending coverage excludes
    processing, unattributed receipts and future arrivals.

    """

    coverage: EventSubscriptionRatePreviewCoverage
    app_id: UUID
    subscription_id: UUID
    observed_at: datetime.datetime
    advisory: bool
    read_only: bool
    expected_revision: int
    """Reviewed bucket revision for the subsequent mutation; other controls and backlog can change independently."""
    current: EventSubscriptionRateLimit
    """Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on
    older responses means unknown."""
    projected: EventSubscriptionRateLimit
    """Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on
    older responses means unknown."""
    paused: bool
    legacy_rate_per_second: int
    circuit_state: str
    capacity_blocking_scopes: list[EventSubscriptionRatePreviewCapacityBlockingScopesItem]
    constraints: list[str]
    """Current pause, circuit, capacity, lease, scheduled-retry, clock and sampling constraints. An empty list does
    not promise future readiness."""
    sample_limit: EventSubscriptionRatePreviewSampleLimit
    sampled_pending: int
    complete: bool
    """Captured pending sample is untruncated with usable timestamps; not evidence of eventual delivery."""
    truncated: bool
    deadline_risk_count: int
    already_expired_count: int
    deadline_risks: list[EventSubscriptionRateDeadlineRisk]
    risks_truncated: bool
    rate_only_drain_millis: int | Unset = UNSET
    """Modeled admission span for the sample, assuming all unexpired sampled recipients are admitted once, no new
    arrivals, oldest-first order and no blocking from pause/circuit/capacity/leases or execution. Includes scheduled
    times and legacy pacing. Missing when clocks or duration are unusable. Truncated samples never estimate the full
    backlog."""

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        advisory = self.advisory

        read_only = self.read_only

        expected_revision = self.expected_revision

        current = self.current.to_dict()

        projected = self.projected.to_dict()

        paused = self.paused

        legacy_rate_per_second = self.legacy_rate_per_second

        circuit_state = self.circuit_state

        capacity_blocking_scopes = []
        for capacity_blocking_scopes_item_data in self.capacity_blocking_scopes:
            capacity_blocking_scopes_item: str = capacity_blocking_scopes_item_data
            capacity_blocking_scopes.append(capacity_blocking_scopes_item)

        constraints = self.constraints

        sample_limit: int = self.sample_limit

        sampled_pending = self.sampled_pending

        complete = self.complete

        truncated = self.truncated

        deadline_risk_count = self.deadline_risk_count

        already_expired_count = self.already_expired_count

        deadline_risks = []
        for deadline_risks_item_data in self.deadline_risks:
            deadline_risks_item = deadline_risks_item_data.to_dict()
            deadline_risks.append(deadline_risks_item)

        risks_truncated = self.risks_truncated

        rate_only_drain_millis = self.rate_only_drain_millis

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "coverage": coverage,
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "advisory": advisory,
                "read_only": read_only,
                "expected_revision": expected_revision,
                "current": current,
                "projected": projected,
                "paused": paused,
                "legacy_rate_per_second": legacy_rate_per_second,
                "circuit_state": circuit_state,
                "capacity_blocking_scopes": capacity_blocking_scopes,
                "constraints": constraints,
                "sample_limit": sample_limit,
                "sampled_pending": sampled_pending,
                "complete": complete,
                "truncated": truncated,
                "deadline_risk_count": deadline_risk_count,
                "already_expired_count": already_expired_count,
                "deadline_risks": deadline_risks,
                "risks_truncated": risks_truncated,
            }
        )
        if rate_only_drain_millis is not UNSET:
            field_dict["rate_only_drain_millis"] = rate_only_drain_millis

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_rate_deadline_risk import EventSubscriptionRateDeadlineRisk
        from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit

        d = dict(src_dict)
        coverage = check_event_subscription_rate_preview_coverage(d.pop("coverage"))

        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        advisory = d.pop("advisory")

        read_only = d.pop("read_only")

        expected_revision = d.pop("expected_revision")

        current = EventSubscriptionRateLimit.from_dict(d.pop("current"))

        projected = EventSubscriptionRateLimit.from_dict(d.pop("projected"))

        paused = d.pop("paused")

        legacy_rate_per_second = d.pop("legacy_rate_per_second")

        circuit_state = d.pop("circuit_state")

        capacity_blocking_scopes = []
        _capacity_blocking_scopes = d.pop("capacity_blocking_scopes")
        for capacity_blocking_scopes_item_data in _capacity_blocking_scopes:
            capacity_blocking_scopes_item = check_event_subscription_rate_preview_capacity_blocking_scopes_item(
                capacity_blocking_scopes_item_data
            )

            capacity_blocking_scopes.append(capacity_blocking_scopes_item)

        constraints = cast(list[str], d.pop("constraints"))

        sample_limit = check_event_subscription_rate_preview_sample_limit(d.pop("sample_limit"))

        sampled_pending = d.pop("sampled_pending")

        complete = d.pop("complete")

        truncated = d.pop("truncated")

        deadline_risk_count = d.pop("deadline_risk_count")

        already_expired_count = d.pop("already_expired_count")

        deadline_risks = []
        _deadline_risks = d.pop("deadline_risks")
        for deadline_risks_item_data in _deadline_risks:
            deadline_risks_item = EventSubscriptionRateDeadlineRisk.from_dict(deadline_risks_item_data)

            deadline_risks.append(deadline_risks_item)

        risks_truncated = d.pop("risks_truncated")

        rate_only_drain_millis = d.pop("rate_only_drain_millis", UNSET)

        event_subscription_rate_preview = cls(
            coverage=coverage,
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            advisory=advisory,
            read_only=read_only,
            expected_revision=expected_revision,
            current=current,
            projected=projected,
            paused=paused,
            legacy_rate_per_second=legacy_rate_per_second,
            circuit_state=circuit_state,
            capacity_blocking_scopes=capacity_blocking_scopes,
            constraints=constraints,
            sample_limit=sample_limit,
            sampled_pending=sampled_pending,
            complete=complete,
            truncated=truncated,
            deadline_risk_count=deadline_risk_count,
            already_expired_count=already_expired_count,
            deadline_risks=deadline_risks,
            risks_truncated=risks_truncated,
            rate_only_drain_millis=rate_only_drain_millis,
        )

        return event_subscription_rate_preview
