from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_circuit_status_state import (
    RealtimeConsumerGroupCircuitStatusState,
    check_realtime_consumer_group_circuit_status_state,
)

T = TypeVar("T", bound="RealtimeConsumerGroupCircuitStatus")


@_attrs_define
class RealtimeConsumerGroupCircuitStatus:
    """Realtime Consumer Group Circuit Status."""

    enabled: bool
    failure_threshold: int
    cooldown_seconds: int
    state: RealtimeConsumerGroupCircuitStatusState
    reason: str
    consecutive_failures: int
    next_probe_at: datetime.datetime | None
    probe_sequence: None | str
    blocked: bool
    """Breaker admission gate only; manual pause and other eligibility checks are separate."""
    retry_after_milliseconds: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        failure_threshold = self.failure_threshold

        cooldown_seconds = self.cooldown_seconds

        state: str = self.state

        reason = self.reason

        consecutive_failures = self.consecutive_failures

        next_probe_at: None | str
        if isinstance(self.next_probe_at, datetime.datetime):
            next_probe_at = self.next_probe_at.isoformat()
        else:
            next_probe_at = self.next_probe_at

        probe_sequence: None | str
        probe_sequence = self.probe_sequence

        blocked = self.blocked

        retry_after_milliseconds = self.retry_after_milliseconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "failure_threshold": failure_threshold,
                "cooldown_seconds": cooldown_seconds,
                "state": state,
                "reason": reason,
                "consecutive_failures": consecutive_failures,
                "next_probe_at": next_probe_at,
                "probe_sequence": probe_sequence,
                "blocked": blocked,
                "retry_after_milliseconds": retry_after_milliseconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        failure_threshold = d.pop("failure_threshold")

        cooldown_seconds = d.pop("cooldown_seconds")

        state = check_realtime_consumer_group_circuit_status_state(d.pop("state"))

        reason = d.pop("reason")

        consecutive_failures = d.pop("consecutive_failures")

        def _parse_next_probe_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_probe_at_type_0 = datetime.datetime.fromisoformat(data)

                return next_probe_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        next_probe_at = _parse_next_probe_at(d.pop("next_probe_at"))

        def _parse_probe_sequence(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        probe_sequence = _parse_probe_sequence(d.pop("probe_sequence"))

        blocked = d.pop("blocked")

        retry_after_milliseconds = d.pop("retry_after_milliseconds")

        realtime_consumer_group_circuit_status = cls(
            enabled=enabled,
            failure_threshold=failure_threshold,
            cooldown_seconds=cooldown_seconds,
            state=state,
            reason=reason,
            consecutive_failures=consecutive_failures,
            next_probe_at=next_probe_at,
            probe_sequence=probe_sequence,
            blocked=blocked,
            retry_after_milliseconds=retry_after_milliseconds,
        )

        realtime_consumer_group_circuit_status.additional_properties = d
        return realtime_consumer_group_circuit_status

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
