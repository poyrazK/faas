from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_delivery_inspection_business_state import (
    OperationDeliveryInspectionBusinessState,
    check_operation_delivery_inspection_business_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationDeliveryInspection")


@_attrs_define
class OperationDeliveryInspection:
    """Sanitized completion transport snapshot; business state remains independently authoritative."""

    operation_id: UUID
    business_state: OperationDeliveryInspectionBusinessState
    operation_expires_at: datetime.datetime
    observed_at: datetime.datetime
    state: str
    attempts: int
    last_response_code: int
    delivery_id: UUID | Unset = UNSET
    webhook_id: UUID | Unset = UNSET
    replay_generation: int | Unset = UNSET
    error_code: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    delivered_at: datetime.datetime | Unset = UNSET
    receiver_state: str | Unset = UNSET
    receiver_cooldown_until: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        business_state: str = self.business_state

        operation_expires_at = self.operation_expires_at.isoformat()

        observed_at = self.observed_at.isoformat()

        state = self.state

        attempts = self.attempts

        last_response_code = self.last_response_code

        delivery_id: str | Unset = UNSET
        if not isinstance(self.delivery_id, Unset):
            delivery_id = str(self.delivery_id)

        webhook_id: str | Unset = UNSET
        if not isinstance(self.webhook_id, Unset):
            webhook_id = str(self.webhook_id)

        replay_generation = self.replay_generation

        error_code = self.error_code

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        delivered_at: str | Unset = UNSET
        if not isinstance(self.delivered_at, Unset):
            delivered_at = self.delivered_at.isoformat()

        receiver_state = self.receiver_state

        receiver_cooldown_until: str | Unset = UNSET
        if not isinstance(self.receiver_cooldown_until, Unset):
            receiver_cooldown_until = self.receiver_cooldown_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "business_state": business_state,
                "operation_expires_at": operation_expires_at,
                "observed_at": observed_at,
                "state": state,
                "attempts": attempts,
                "last_response_code": last_response_code,
            }
        )
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if webhook_id is not UNSET:
            field_dict["webhook_id"] = webhook_id
        if replay_generation is not UNSET:
            field_dict["replay_generation"] = replay_generation
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if delivered_at is not UNSET:
            field_dict["delivered_at"] = delivered_at
        if receiver_state is not UNSET:
            field_dict["receiver_state"] = receiver_state
        if receiver_cooldown_until is not UNSET:
            field_dict["receiver_cooldown_until"] = receiver_cooldown_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        business_state = check_operation_delivery_inspection_business_state(d.pop("business_state"))

        operation_expires_at = datetime.datetime.fromisoformat(d.pop("operation_expires_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        state = d.pop("state")

        attempts = d.pop("attempts")

        last_response_code = d.pop("last_response_code")

        _delivery_id = d.pop("delivery_id", UNSET)
        delivery_id: UUID | Unset
        if isinstance(_delivery_id, Unset):
            delivery_id = UNSET
        else:
            delivery_id = UUID(_delivery_id)

        _webhook_id = d.pop("webhook_id", UNSET)
        webhook_id: UUID | Unset
        if isinstance(_webhook_id, Unset):
            webhook_id = UNSET
        else:
            webhook_id = UUID(_webhook_id)

        replay_generation = d.pop("replay_generation", UNSET)

        error_code = d.pop("error_code", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _delivered_at = d.pop("delivered_at", UNSET)
        delivered_at: datetime.datetime | Unset
        if isinstance(_delivered_at, Unset):
            delivered_at = UNSET
        else:
            delivered_at = datetime.datetime.fromisoformat(_delivered_at)

        receiver_state = d.pop("receiver_state", UNSET)

        _receiver_cooldown_until = d.pop("receiver_cooldown_until", UNSET)
        receiver_cooldown_until: datetime.datetime | Unset
        if isinstance(_receiver_cooldown_until, Unset):
            receiver_cooldown_until = UNSET
        else:
            receiver_cooldown_until = datetime.datetime.fromisoformat(_receiver_cooldown_until)

        operation_delivery_inspection = cls(
            operation_id=operation_id,
            business_state=business_state,
            operation_expires_at=operation_expires_at,
            observed_at=observed_at,
            state=state,
            attempts=attempts,
            last_response_code=last_response_code,
            delivery_id=delivery_id,
            webhook_id=webhook_id,
            replay_generation=replay_generation,
            error_code=error_code,
            next_attempt_at=next_attempt_at,
            delivered_at=delivered_at,
            receiver_state=receiver_state,
            receiver_cooldown_until=receiver_cooldown_until,
        )

        return operation_delivery_inspection
