from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_message import RealtimeConsumerGroupMessage


T = TypeVar("T", bound="RealtimeConsumerGroupClaim")


@_attrs_define
class RealtimeConsumerGroupClaim:
    """Realtime Consumer Group Claim."""

    message: RealtimeConsumerGroupMessage
    """Realtime Consumer Group Message."""
    worker: str
    attempt: int
    claim_token: str
    """Opaque lease credential. Never log or persist in generic response caches."""
    lease_until: datetime.datetime
    group_id: UUID
    """Immutable counter namespace. A recreated group has a different UUID."""
    fencing_token: str
    """Monotonically increasing per-group signed-64-bit counter. Stable on renewal, new on every new lease
    including retry/replay. Compare with BigInt, within a group UUID and resource scope; downstream storage must
    enforce stale-write rejection."""
    delivery_deadline: datetime.datetime | None
    """Frozen deadline, or null. lease_until never extends beyond it. Handlers must stop cooperatively and guard
    external writes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        message = self.message.to_dict()

        worker = self.worker

        attempt = self.attempt

        claim_token = self.claim_token

        lease_until = self.lease_until.isoformat()

        group_id = str(self.group_id)

        fencing_token = self.fencing_token

        delivery_deadline: None | str
        if isinstance(self.delivery_deadline, datetime.datetime):
            delivery_deadline = self.delivery_deadline.isoformat()
        else:
            delivery_deadline = self.delivery_deadline

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "message": message,
                "worker": worker,
                "attempt": attempt,
                "claim_token": claim_token,
                "lease_until": lease_until,
                "group_id": group_id,
                "fencing_token": fencing_token,
                "delivery_deadline": delivery_deadline,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_message import RealtimeConsumerGroupMessage

        d = dict(src_dict)
        message = RealtimeConsumerGroupMessage.from_dict(d.pop("message"))

        worker = d.pop("worker")

        attempt = d.pop("attempt")

        claim_token = d.pop("claim_token")

        lease_until = datetime.datetime.fromisoformat(d.pop("lease_until"))

        group_id = UUID(d.pop("group_id"))

        fencing_token = d.pop("fencing_token")

        def _parse_delivery_deadline(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                delivery_deadline_type_0 = datetime.datetime.fromisoformat(data)

                return delivery_deadline_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        delivery_deadline = _parse_delivery_deadline(d.pop("delivery_deadline"))

        realtime_consumer_group_claim = cls(
            message=message,
            worker=worker,
            attempt=attempt,
            claim_token=claim_token,
            lease_until=lease_until,
            group_id=group_id,
            fencing_token=fencing_token,
            delivery_deadline=delivery_deadline,
        )

        realtime_consumer_group_claim.additional_properties = d
        return realtime_consumer_group_claim

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
