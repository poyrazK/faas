from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeDeadLetterPreviewItem")


@_attrs_define
class RealtimeDeadLetterPreviewItem:
    """Realtime Dead Letter Preview Item."""

    sequence: str
    dead_since: datetime.datetime
    attempts: int
    failure_code: str
    ordering_blocked: bool
    """A current unexpired same-key lease blocks replay; observed only."""
    expires_at: datetime.datetime | None
    """Nominal retention expiry; warning grace may postpone actual cleanup."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        dead_since = self.dead_since.isoformat()

        attempts = self.attempts

        failure_code = self.failure_code

        ordering_blocked = self.ordering_blocked

        expires_at: None | str
        if isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "dead_since": dead_since,
                "attempts": attempts,
                "failure_code": failure_code,
                "ordering_blocked": ordering_blocked,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        dead_since = datetime.datetime.fromisoformat(d.pop("dead_since"))

        attempts = d.pop("attempts")

        failure_code = d.pop("failure_code")

        ordering_blocked = d.pop("ordering_blocked")

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

        realtime_dead_letter_preview_item = cls(
            sequence=sequence,
            dead_since=dead_since,
            attempts=attempts,
            failure_code=failure_code,
            ordering_blocked=ordering_blocked,
            expires_at=expires_at,
        )

        realtime_dead_letter_preview_item.additional_properties = d
        return realtime_dead_letter_preview_item

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
