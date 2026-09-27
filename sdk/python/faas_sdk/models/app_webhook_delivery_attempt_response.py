from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_webhook_delivery_attempt_response_outcome import (
    AppWebhookDeliveryAttemptResponseOutcome,
    check_app_webhook_delivery_attempt_response_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppWebhookDeliveryAttemptResponse")


@_attrs_define
class AppWebhookDeliveryAttemptResponse:
    """One completed outbound dispatch, including its replay round and receiver outcome."""

    id: UUID
    delivery_id: UUID
    replay_generation: int
    attempt_number: int
    outcome: AppWebhookDeliveryAttemptResponseOutcome
    response_code: int
    started_at: datetime.datetime
    finished_at: datetime.datetime
    duration_ms: int
    error: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        delivery_id = str(self.delivery_id)

        replay_generation = self.replay_generation

        attempt_number = self.attempt_number

        outcome: str = self.outcome

        response_code = self.response_code

        started_at = self.started_at.isoformat()

        finished_at = self.finished_at.isoformat()

        duration_ms = self.duration_ms

        error = self.error

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "delivery_id": delivery_id,
                "replay_generation": replay_generation,
                "attempt_number": attempt_number,
                "outcome": outcome,
                "response_code": response_code,
                "started_at": started_at,
                "finished_at": finished_at,
                "duration_ms": duration_ms,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        delivery_id = UUID(d.pop("delivery_id"))

        replay_generation = d.pop("replay_generation")

        attempt_number = d.pop("attempt_number")

        outcome = check_app_webhook_delivery_attempt_response_outcome(d.pop("outcome"))

        response_code = d.pop("response_code")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        duration_ms = d.pop("duration_ms")

        error = d.pop("error", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        app_webhook_delivery_attempt_response = cls(
            id=id,
            delivery_id=delivery_id,
            replay_generation=replay_generation,
            attempt_number=attempt_number,
            outcome=outcome,
            response_code=response_code,
            started_at=started_at,
            finished_at=finished_at,
            duration_ms=duration_ms,
            error=error,
            next_attempt_at=next_attempt_at,
        )

        app_webhook_delivery_attempt_response.additional_properties = d
        return app_webhook_delivery_attempt_response

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
