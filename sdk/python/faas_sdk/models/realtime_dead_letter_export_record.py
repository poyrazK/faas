from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_dead_letter_export_record_type import (
    RealtimeDeadLetterExportRecordType,
    check_realtime_dead_letter_export_record_type,
)

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_message import RealtimeConsumerGroupMessage


T = TypeVar("T", bound="RealtimeDeadLetterExportRecord")


@_attrs_define
class RealtimeDeadLetterExportRecord:
    """Realtime Dead Letter Export Record."""

    type_: RealtimeDeadLetterExportRecordType
    group_id: UUID
    sequence: str
    dead_since: datetime.datetime
    attempts: int
    failure_code: str
    fencing_token: None | str
    delivery_deadline: datetime.datetime | None
    deadline_invalid: bool
    ordering_key: str
    priority: int
    expires_at: datetime.datetime | None
    """Nominal dead-letter expiry; retained rows can survive until cleanup runs"""
    message: RealtimeConsumerGroupMessage
    """Realtime Consumer Group Message."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        group_id = str(self.group_id)

        sequence = self.sequence

        dead_since = self.dead_since.isoformat()

        attempts = self.attempts

        failure_code = self.failure_code

        fencing_token: None | str
        fencing_token = self.fencing_token

        delivery_deadline: None | str
        if isinstance(self.delivery_deadline, datetime.datetime):
            delivery_deadline = self.delivery_deadline.isoformat()
        else:
            delivery_deadline = self.delivery_deadline

        deadline_invalid = self.deadline_invalid

        ordering_key = self.ordering_key

        priority = self.priority

        expires_at: None | str
        if isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        message = self.message.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "group_id": group_id,
                "sequence": sequence,
                "dead_since": dead_since,
                "attempts": attempts,
                "failure_code": failure_code,
                "fencing_token": fencing_token,
                "delivery_deadline": delivery_deadline,
                "deadline_invalid": deadline_invalid,
                "ordering_key": ordering_key,
                "priority": priority,
                "expires_at": expires_at,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_message import RealtimeConsumerGroupMessage

        d = dict(src_dict)
        type_ = check_realtime_dead_letter_export_record_type(d.pop("type"))

        group_id = UUID(d.pop("group_id"))

        sequence = d.pop("sequence")

        dead_since = datetime.datetime.fromisoformat(d.pop("dead_since"))

        attempts = d.pop("attempts")

        failure_code = d.pop("failure_code")

        def _parse_fencing_token(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        fencing_token = _parse_fencing_token(d.pop("fencing_token"))

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

        deadline_invalid = d.pop("deadline_invalid")

        ordering_key = d.pop("ordering_key")

        priority = d.pop("priority")

        def _parse_expires_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        expires_at = _parse_expires_at(d.pop("expires_at"))

        message = RealtimeConsumerGroupMessage.from_dict(d.pop("message"))

        realtime_dead_letter_export_record = cls(
            type_=type_,
            group_id=group_id,
            sequence=sequence,
            dead_since=dead_since,
            attempts=attempts,
            failure_code=failure_code,
            fencing_token=fencing_token,
            delivery_deadline=delivery_deadline,
            deadline_invalid=deadline_invalid,
            ordering_key=ordering_key,
            priority=priority,
            expires_at=expires_at,
            message=message,
        )

        realtime_dead_letter_export_record.additional_properties = d
        return realtime_dead_letter_export_record

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
