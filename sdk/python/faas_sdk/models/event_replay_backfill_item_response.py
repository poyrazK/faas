from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_replay_backfill_item_response_state import (
    EventReplayBackfillItemResponseState,
    check_event_replay_backfill_item_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventReplayBackfillItemResponse")


@_attrs_define
class EventReplayBackfillItemResponse:
    """Metadata-only outcome for one envelope included in a durable backfill."""

    event_source: str
    event_id: str
    event_type: str
    accepted_at: datetime.datetime
    state: EventReplayBackfillItemResponseState
    attempts: int
    retryable: bool
    updated_at: datetime.datetime
    schema_version: str | Unset = UNSET
    failure_code: str | Unset = UNSET
    last_error: str | Unset = UNSET
    """Routing diagnostic clipped to at most 1024 UTF-8 bytes."""
    details_truncated: bool | Unset = UNSET
    """Whether the failure code or routing diagnostic was clipped."""
    receipt_url: str | Unset = UNSET
    """Account-authenticated receipt inspection; omitted after the original receipt expires."""
    attempt_history_url: str | Unset = UNSET
    """Handler attempt history for this consumer; omitted when retained delivery provenance or current app
    ownership is unavailable."""

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        event_type = self.event_type

        accepted_at = self.accepted_at.isoformat()

        state: str = self.state

        attempts = self.attempts

        retryable = self.retryable

        updated_at = self.updated_at.isoformat()

        schema_version = self.schema_version

        failure_code = self.failure_code

        last_error = self.last_error

        details_truncated = self.details_truncated

        receipt_url = self.receipt_url

        attempt_history_url = self.attempt_history_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "event_type": event_type,
                "accepted_at": accepted_at,
                "state": state,
                "attempts": attempts,
                "retryable": retryable,
                "updated_at": updated_at,
            }
        )
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if details_truncated is not UNSET:
            field_dict["details_truncated"] = details_truncated
        if receipt_url is not UNSET:
            field_dict["receipt_url"] = receipt_url
        if attempt_history_url is not UNSET:
            field_dict["attempt_history_url"] = attempt_history_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        event_type = d.pop("event_type")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        state = check_event_replay_backfill_item_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        retryable = d.pop("retryable")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        schema_version = d.pop("schema_version", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        last_error = d.pop("last_error", UNSET)

        details_truncated = d.pop("details_truncated", UNSET)

        receipt_url = d.pop("receipt_url", UNSET)

        attempt_history_url = d.pop("attempt_history_url", UNSET)

        event_replay_backfill_item_response = cls(
            event_source=event_source,
            event_id=event_id,
            event_type=event_type,
            accepted_at=accepted_at,
            state=state,
            attempts=attempts,
            retryable=retryable,
            updated_at=updated_at,
            schema_version=schema_version,
            failure_code=failure_code,
            last_error=last_error,
            details_truncated=details_truncated,
            receipt_url=receipt_url,
            attempt_history_url=attempt_history_url,
        )

        return event_replay_backfill_item_response
