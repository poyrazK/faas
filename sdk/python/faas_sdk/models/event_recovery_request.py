from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_request_mode import EventRecoveryRequestMode, check_event_recovery_request_mode
from ..models.event_recovery_request_outcome import EventRecoveryRequestOutcome, check_event_recovery_request_outcome
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryRequest")


@_attrs_define
class EventRecoveryRequest:
    """Select routing failures (default) or the latest replayable retained execution per application event consumer.
    Execution mode includes publication and materialized backfill recipients, excludes workflows and object
    notifications, and requires retained admission and execution records. Creation freezes its own selection; preview is
    advisory.

    """

    reason: str | Unset = UNSET
    """Optional operator reason, limited to 512 UTF-8 bytes without control characters. Stored only in audit
    history; omitted from frozen selection. Preview does not record it."""
    mode: EventRecoveryRequestMode | Unset = "routing"
    outcome: EventRecoveryRequestOutcome | Unset = UNSET
    """Execution mode only; omitted selects both replayable outcomes."""
    subscription_id: str | Unset = UNSET
    event_source: str | Unset = UNSET
    event_type: str | Unset = UNSET
    failure_code: str | Unset = UNSET
    min_age_seconds: int | Unset = 0
    """Minimum age of the recorded terminal failure."""
    protect_receipts: bool | Unset = False
    """Hold selected retained receipts from pruning while their items are pending and the job is active and
    unexpired. Pausing does not extend the existing 24-hour lifetime. Preview acquires no holds. Held receipts still
    count toward account storage limits."""
    include_non_retryable: bool | Unset = False
    rate_per_second: int | Unset = 10
    """Maximum recipients processed per job in a one-second window; zero uses the default. Actual throughput
    depends on scheduler load."""

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        outcome: str | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome

        subscription_id = self.subscription_id

        event_source = self.event_source

        event_type = self.event_type

        failure_code = self.failure_code

        min_age_seconds = self.min_age_seconds

        protect_receipts = self.protect_receipts

        include_non_retryable = self.include_non_retryable

        rate_per_second = self.rate_per_second

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if reason is not UNSET:
            field_dict["reason"] = reason
        if mode is not UNSET:
            field_dict["mode"] = mode
        if outcome is not UNSET:
            field_dict["outcome"] = outcome
        if subscription_id is not UNSET:
            field_dict["subscription_id"] = subscription_id
        if event_source is not UNSET:
            field_dict["event_source"] = event_source
        if event_type is not UNSET:
            field_dict["event_type"] = event_type
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if min_age_seconds is not UNSET:
            field_dict["min_age_seconds"] = min_age_seconds
        if protect_receipts is not UNSET:
            field_dict["protect_receipts"] = protect_receipts
        if include_non_retryable is not UNSET:
            field_dict["include_non_retryable"] = include_non_retryable
        if rate_per_second is not UNSET:
            field_dict["rate_per_second"] = rate_per_second

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = d.pop("reason", UNSET)

        _mode = d.pop("mode", UNSET)
        mode: EventRecoveryRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_event_recovery_request_mode(_mode)

        _outcome = d.pop("outcome", UNSET)
        outcome: EventRecoveryRequestOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = check_event_recovery_request_outcome(_outcome)

        subscription_id = d.pop("subscription_id", UNSET)

        event_source = d.pop("event_source", UNSET)

        event_type = d.pop("event_type", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        min_age_seconds = d.pop("min_age_seconds", UNSET)

        protect_receipts = d.pop("protect_receipts", UNSET)

        include_non_retryable = d.pop("include_non_retryable", UNSET)

        rate_per_second = d.pop("rate_per_second", UNSET)

        event_recovery_request = cls(
            reason=reason,
            mode=mode,
            outcome=outcome,
            subscription_id=subscription_id,
            event_source=event_source,
            event_type=event_type,
            failure_code=failure_code,
            min_age_seconds=min_age_seconds,
            protect_receipts=protect_receipts,
            include_non_retryable=include_non_retryable,
            rate_per_second=rate_per_second,
        )

        return event_recovery_request
