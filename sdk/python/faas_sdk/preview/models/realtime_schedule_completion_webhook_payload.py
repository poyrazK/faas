from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_schedule_completion_webhook_payload_outcome import (
    RealtimeScheduleCompletionWebhookPayloadOutcome,
    check_realtime_schedule_completion_webhook_payload_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeScheduleCompletionWebhookPayload")


@_attrs_define
class RealtimeScheduleCompletionWebhookPayload:
    """Committed occurrence outcome delivered through app webhooks. Recurring publications and skips emit independently of
    the next occurrence's status; failures emit only when the retry budget is exhausted.

    """

    event_id: str
    app_id: str
    endpoint_id: str
    channel: str
    schedule_id: str
    version: int
    occurrence: int
    completed_occurrences: int
    skipped_occurrences: int
    outcome: RealtimeScheduleCompletionWebhookPayloadOutcome
    attempts: int
    cycle_attempts: int
    deliver_at: datetime.datetime
    occurred_at: datetime.datetime
    sequence: int | Unset = UNSET
    failure_code: str | Unset = UNSET
    skip_reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        app_id = self.app_id

        endpoint_id = self.endpoint_id

        channel = self.channel

        schedule_id = self.schedule_id

        version = self.version

        occurrence = self.occurrence

        completed_occurrences = self.completed_occurrences

        skipped_occurrences = self.skipped_occurrences

        outcome: str = self.outcome

        attempts = self.attempts

        cycle_attempts = self.cycle_attempts

        deliver_at = self.deliver_at.isoformat()

        occurred_at = self.occurred_at.isoformat()

        sequence = self.sequence

        failure_code = self.failure_code

        skip_reason = self.skip_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "app_id": app_id,
                "endpoint_id": endpoint_id,
                "channel": channel,
                "schedule_id": schedule_id,
                "version": version,
                "occurrence": occurrence,
                "completed_occurrences": completed_occurrences,
                "skipped_occurrences": skipped_occurrences,
                "outcome": outcome,
                "attempts": attempts,
                "cycle_attempts": cycle_attempts,
                "deliver_at": deliver_at,
                "occurred_at": occurred_at,
            }
        )
        if sequence is not UNSET:
            field_dict["sequence"] = sequence
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if skip_reason is not UNSET:
            field_dict["skip_reason"] = skip_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        app_id = d.pop("app_id")

        endpoint_id = d.pop("endpoint_id")

        channel = d.pop("channel")

        schedule_id = d.pop("schedule_id")

        version = d.pop("version")

        occurrence = d.pop("occurrence")

        completed_occurrences = d.pop("completed_occurrences")

        skipped_occurrences = d.pop("skipped_occurrences")

        outcome = check_realtime_schedule_completion_webhook_payload_outcome(d.pop("outcome"))

        attempts = d.pop("attempts")

        cycle_attempts = d.pop("cycle_attempts")

        deliver_at = datetime.datetime.fromisoformat(d.pop("deliver_at"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        sequence = d.pop("sequence", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        skip_reason = d.pop("skip_reason", UNSET)

        realtime_schedule_completion_webhook_payload = cls(
            event_id=event_id,
            app_id=app_id,
            endpoint_id=endpoint_id,
            channel=channel,
            schedule_id=schedule_id,
            version=version,
            occurrence=occurrence,
            completed_occurrences=completed_occurrences,
            skipped_occurrences=skipped_occurrences,
            outcome=outcome,
            attempts=attempts,
            cycle_attempts=cycle_attempts,
            deliver_at=deliver_at,
            occurred_at=occurred_at,
            sequence=sequence,
            failure_code=failure_code,
            skip_reason=skip_reason,
        )

        realtime_schedule_completion_webhook_payload.additional_properties = d
        return realtime_schedule_completion_webhook_payload

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
