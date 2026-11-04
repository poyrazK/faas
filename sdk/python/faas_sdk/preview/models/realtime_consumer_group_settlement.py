from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_settlement_action import (
    RealtimeConsumerGroupSettlementAction,
    check_realtime_consumer_group_settlement_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeConsumerGroupSettlement")


@_attrs_define
class RealtimeConsumerGroupSettlement:
    """Realtime Consumer Group Settlement."""

    sequence: str
    claim_token: str
    action: RealtimeConsumerGroupSettlementAction
    idempotency_key: UUID | Unset = UNSET
    """this realtime operation. Optional UUID. Retains the original successful result for 24 hours for this group
    and principal. Reuse requires identical normalized contents, including claim tokens and ordered batch items. At
    most 1024 receipts per group; full capacity returns 429 before settlement. Omit item-level keys in batches.
    Replays require current authorization and do not settle again. Renew receipts report the original lease expiry
    rather than extending it again."""
    retry_after_seconds: int | Unset = UNSET
    """Omitted uses enabled group backoff; explicit zero retries immediately."""
    failure_code: str | Unset = UNSET
    """Optional printable ASCII diagnostic code; do not include secrets."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        claim_token = self.claim_token

        action: str = self.action

        idempotency_key: str | Unset = UNSET
        if not isinstance(self.idempotency_key, Unset):
            idempotency_key = str(self.idempotency_key)

        retry_after_seconds = self.retry_after_seconds

        failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "claim_token": claim_token,
                "action": action,
            }
        )
        if idempotency_key is not UNSET:
            field_dict["idempotency_key"] = idempotency_key
        if retry_after_seconds is not UNSET:
            field_dict["retry_after_seconds"] = retry_after_seconds
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        claim_token = d.pop("claim_token")

        action = check_realtime_consumer_group_settlement_action(d.pop("action"))

        _idempotency_key = d.pop("idempotency_key", UNSET)
        idempotency_key: UUID | Unset
        if isinstance(_idempotency_key, Unset):
            idempotency_key = UNSET
        else:
            idempotency_key = UUID(_idempotency_key)

        retry_after_seconds = d.pop("retry_after_seconds", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        realtime_consumer_group_settlement = cls(
            sequence=sequence,
            claim_token=claim_token,
            action=action,
            idempotency_key=idempotency_key,
            retry_after_seconds=retry_after_seconds,
            failure_code=failure_code,
        )

        realtime_consumer_group_settlement.additional_properties = d
        return realtime_consumer_group_settlement

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
