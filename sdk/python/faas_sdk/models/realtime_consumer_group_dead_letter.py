from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_message import RealtimeConsumerGroupMessage


T = TypeVar("T", bound="RealtimeConsumerGroupDeadLetter")


@_attrs_define
class RealtimeConsumerGroupDeadLetter:
    """Realtime Consumer Group Dead Letter."""

    message: RealtimeConsumerGroupMessage
    """Realtime Consumer Group Message."""
    attempts: int
    failure_code: str
    retention_started_at: datetime.datetime | None
    expires_at: datetime.datetime | None
    warning_at: datetime.datetime | None
    cleanup_after: datetime.datetime | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        message = self.message.to_dict()

        attempts = self.attempts

        failure_code = self.failure_code

        retention_started_at: None | str
        if isinstance(self.retention_started_at, datetime.datetime):
            retention_started_at = self.retention_started_at.isoformat()
        else:
            retention_started_at = self.retention_started_at

        expires_at: None | str
        if isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        warning_at: None | str
        if isinstance(self.warning_at, datetime.datetime):
            warning_at = self.warning_at.isoformat()
        else:
            warning_at = self.warning_at

        cleanup_after: None | str
        if isinstance(self.cleanup_after, datetime.datetime):
            cleanup_after = self.cleanup_after.isoformat()
        else:
            cleanup_after = self.cleanup_after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "message": message,
                "attempts": attempts,
                "failure_code": failure_code,
                "retention_started_at": retention_started_at,
                "expires_at": expires_at,
                "warning_at": warning_at,
                "cleanup_after": cleanup_after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_message import RealtimeConsumerGroupMessage

        d = dict(src_dict)
        message = RealtimeConsumerGroupMessage.from_dict(d.pop("message"))

        attempts = d.pop("attempts")

        failure_code = d.pop("failure_code")

        def _parse_retention_started_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                retention_started_at_type_0 = datetime.datetime.fromisoformat(data)

                return retention_started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        retention_started_at = _parse_retention_started_at(d.pop("retention_started_at"))

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

        def _parse_warning_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                warning_at_type_0 = datetime.datetime.fromisoformat(data)

                return warning_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        warning_at = _parse_warning_at(d.pop("warning_at"))

        def _parse_cleanup_after(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                cleanup_after_type_0 = datetime.datetime.fromisoformat(data)

                return cleanup_after_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        cleanup_after = _parse_cleanup_after(d.pop("cleanup_after"))

        realtime_consumer_group_dead_letter = cls(
            message=message,
            attempts=attempts,
            failure_code=failure_code,
            retention_started_at=retention_started_at,
            expires_at=expires_at,
            warning_at=warning_at,
            cleanup_after=cleanup_after,
        )

        realtime_consumer_group_dead_letter.additional_properties = d
        return realtime_consumer_group_dead_letter

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
