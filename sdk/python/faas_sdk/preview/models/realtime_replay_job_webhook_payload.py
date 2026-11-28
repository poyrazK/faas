from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_replay_job_webhook_payload_state import (
    RealtimeReplayJobWebhookPayloadState,
    check_realtime_replay_job_webhook_payload_state,
)

T = TypeVar("T", bound="RealtimeReplayJobWebhookPayload")


@_attrs_define
class RealtimeReplayJobWebhookPayload:
    """Payload of realtime.replay_job.completed, realtime.replay_job.cancelled and realtime.replay_job.processing_failed
    app webhooks. Use event_id to deduplicate delivery retries. Counts describe the committed transition; cancellation
    does not undo earlier replays.

    """

    event_id: UUID
    app_id: UUID
    endpoint_id: UUID
    group_id: UUID
    job_id: UUID
    channel: str
    group: str
    state: RealtimeReplayJobWebhookPayloadState
    rate: int
    selected: int
    processed: int
    replayed: int
    skipped: int
    remaining: int
    error_count: int
    error_code: str
    """processing_failed on the warning event; empty for terminal events. Contains no raw database error."""
    error_threshold: int
    """One warning per job when cumulative processing errors first reach this threshold."""
    inspector_path: str
    """Relative inspector link on the Gregale console origin. Recent job history may later be pruned."""
    occurred_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        app_id = str(self.app_id)

        endpoint_id = str(self.endpoint_id)

        group_id = str(self.group_id)

        job_id = str(self.job_id)

        channel = self.channel

        group = self.group

        state: str = self.state

        rate = self.rate

        selected = self.selected

        processed = self.processed

        replayed = self.replayed

        skipped = self.skipped

        remaining = self.remaining

        error_count = self.error_count

        error_code = self.error_code

        error_threshold = self.error_threshold

        inspector_path = self.inspector_path

        occurred_at = self.occurred_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "app_id": app_id,
                "endpoint_id": endpoint_id,
                "group_id": group_id,
                "job_id": job_id,
                "channel": channel,
                "group": group,
                "state": state,
                "rate": rate,
                "selected": selected,
                "processed": processed,
                "replayed": replayed,
                "skipped": skipped,
                "remaining": remaining,
                "error_count": error_count,
                "error_code": error_code,
                "error_threshold": error_threshold,
                "inspector_path": inspector_path,
                "occurred_at": occurred_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        app_id = UUID(d.pop("app_id"))

        endpoint_id = UUID(d.pop("endpoint_id"))

        group_id = UUID(d.pop("group_id"))

        job_id = UUID(d.pop("job_id"))

        channel = d.pop("channel")

        group = d.pop("group")

        state = check_realtime_replay_job_webhook_payload_state(d.pop("state"))

        rate = d.pop("rate")

        selected = d.pop("selected")

        processed = d.pop("processed")

        replayed = d.pop("replayed")

        skipped = d.pop("skipped")

        remaining = d.pop("remaining")

        error_count = d.pop("error_count")

        error_code = d.pop("error_code")

        error_threshold = d.pop("error_threshold")

        inspector_path = d.pop("inspector_path")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        realtime_replay_job_webhook_payload = cls(
            event_id=event_id,
            app_id=app_id,
            endpoint_id=endpoint_id,
            group_id=group_id,
            job_id=job_id,
            channel=channel,
            group=group,
            state=state,
            rate=rate,
            selected=selected,
            processed=processed,
            replayed=replayed,
            skipped=skipped,
            remaining=remaining,
            error_count=error_count,
            error_code=error_code,
            error_threshold=error_threshold,
            inspector_path=inspector_path,
            occurred_at=occurred_at,
        )

        realtime_replay_job_webhook_payload.additional_properties = d
        return realtime_replay_job_webhook_payload

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
