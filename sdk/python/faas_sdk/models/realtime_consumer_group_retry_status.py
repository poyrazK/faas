from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupRetryStatus")


@_attrs_define
class RealtimeConsumerGroupRetryStatus:
    """Realtime Consumer Group Retry Status."""

    enabled: bool
    base_delay_seconds: int
    max_delay_seconds: int
    jitter: bool
    permanent_failure_codes: list[str]
    next_retry_at: datetime.datetime | None
    """Earliest future available_at among ready snapshots; null when none."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        base_delay_seconds = self.base_delay_seconds

        max_delay_seconds = self.max_delay_seconds

        jitter = self.jitter

        permanent_failure_codes = self.permanent_failure_codes

        next_retry_at: None | str
        if isinstance(self.next_retry_at, datetime.datetime):
            next_retry_at = self.next_retry_at.isoformat()
        else:
            next_retry_at = self.next_retry_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "base_delay_seconds": base_delay_seconds,
                "max_delay_seconds": max_delay_seconds,
                "jitter": jitter,
                "permanent_failure_codes": permanent_failure_codes,
                "next_retry_at": next_retry_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        base_delay_seconds = d.pop("base_delay_seconds")

        max_delay_seconds = d.pop("max_delay_seconds")

        jitter = d.pop("jitter")

        permanent_failure_codes = cast(list[str], d.pop("permanent_failure_codes"))

        def _parse_next_retry_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_retry_at_type_0 = datetime.datetime.fromisoformat(data)

                return next_retry_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        next_retry_at = _parse_next_retry_at(d.pop("next_retry_at"))

        realtime_consumer_group_retry_status = cls(
            enabled=enabled,
            base_delay_seconds=base_delay_seconds,
            max_delay_seconds=max_delay_seconds,
            jitter=jitter,
            permanent_failure_codes=permanent_failure_codes,
            next_retry_at=next_retry_at,
        )

        realtime_consumer_group_retry_status.additional_properties = d
        return realtime_consumer_group_retry_status

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
