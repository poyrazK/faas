from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationDeliveryRetryRequest")


@_attrs_define
class OperationDeliveryRetryRequest:
    """Stable retry identity and the observed dead delivery generation; explicit generation zero is valid."""

    retry_id: str
    delivery_id: UUID
    expected_replay_generation: int

    def to_dict(self) -> dict[str, Any]:
        retry_id = self.retry_id

        delivery_id = str(self.delivery_id)

        expected_replay_generation = self.expected_replay_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retry_id": retry_id,
                "delivery_id": delivery_id,
                "expected_replay_generation": expected_replay_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retry_id = d.pop("retry_id")

        delivery_id = UUID(d.pop("delivery_id"))

        expected_replay_generation = d.pop("expected_replay_generation")

        operation_delivery_retry_request = cls(
            retry_id=retry_id,
            delivery_id=delivery_id,
            expected_replay_generation=expected_replay_generation,
        )

        return operation_delivery_retry_request
