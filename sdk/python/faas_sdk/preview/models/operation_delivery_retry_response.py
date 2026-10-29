from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_delivery_retry_response_state import (
    OperationDeliveryRetryResponseState,
    check_operation_delivery_retry_response_state,
)

T = TypeVar("T", bound="OperationDeliveryRetryResponse")


@_attrs_define
class OperationDeliveryRetryResponse:
    """Immutable notification reset decision retained with its parent operation; queued is not delivery success."""

    operation_id: UUID
    retry_id: str
    delivery_id: UUID
    expected_replay_generation: int
    replay_generation: int
    state: OperationDeliveryRetryResponseState
    queued_at: datetime.datetime
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        retry_id = self.retry_id

        delivery_id = str(self.delivery_id)

        expected_replay_generation = self.expected_replay_generation

        replay_generation = self.replay_generation

        state: str = self.state

        queued_at = self.queued_at.isoformat()

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "retry_id": retry_id,
                "delivery_id": delivery_id,
                "expected_replay_generation": expected_replay_generation,
                "replay_generation": replay_generation,
                "state": state,
                "queued_at": queued_at,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        retry_id = d.pop("retry_id")

        delivery_id = UUID(d.pop("delivery_id"))

        expected_replay_generation = d.pop("expected_replay_generation")

        replay_generation = d.pop("replay_generation")

        state = check_operation_delivery_retry_response_state(d.pop("state"))

        queued_at = datetime.datetime.fromisoformat(d.pop("queued_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        operation_delivery_retry_response = cls(
            operation_id=operation_id,
            retry_id=retry_id,
            delivery_id=delivery_id,
            expected_replay_generation=expected_replay_generation,
            replay_generation=replay_generation,
            state=state,
            queued_at=queued_at,
            expires_at=expires_at,
        )

        return operation_delivery_retry_response
