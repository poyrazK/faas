from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_route_canary_reason_type_0 import (
    OutboundAIRouteCanaryReasonType0,
    check_outbound_ai_route_canary_reason_type_0,
)
from ..models.outbound_ai_route_canary_state import OutboundAIRouteCanaryState, check_outbound_ai_route_canary_state

T = TypeVar("T", bound="OutboundAIRouteCanary")


@_attrs_define
class OutboundAIRouteCanary:
    """A route's model canary. target is the 1-based route position tried first by percent of the route's calls, chosen per
    X-Gregale-AI-Session, else X-Gregale-AI-User, else at random. The outbound gateway rolls the canary back when, after
    min_calls canary attempts in the window, its error rate exceeds the first target's by more than
    max_error_rate_increase percentage points, or its mean time to first byte by more than max_latency_increase_percent.

    """

    target: int
    percent: int
    max_error_rate_increase: int
    max_latency_increase_percent: int | None
    min_calls: int
    window_minutes: int
    state: OutboundAIRouteCanaryState
    reason: None | OutboundAIRouteCanaryReasonType0
    started_at: datetime.datetime
    decided_at: datetime.datetime | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target = self.target

        percent = self.percent

        max_error_rate_increase = self.max_error_rate_increase

        max_latency_increase_percent: int | None
        max_latency_increase_percent = self.max_latency_increase_percent

        min_calls = self.min_calls

        window_minutes = self.window_minutes

        state: str = self.state

        reason: None | str
        if isinstance(self.reason, str):
            reason = self.reason
        else:
            reason = self.reason

        started_at = self.started_at.isoformat()

        decided_at: None | str
        if isinstance(self.decided_at, datetime.datetime):
            decided_at = self.decided_at.isoformat()
        else:
            decided_at = self.decided_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target": target,
                "percent": percent,
                "max_error_rate_increase": max_error_rate_increase,
                "max_latency_increase_percent": max_latency_increase_percent,
                "min_calls": min_calls,
                "window_minutes": window_minutes,
                "state": state,
                "reason": reason,
                "started_at": started_at,
                "decided_at": decided_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target = d.pop("target")

        percent = d.pop("percent")

        max_error_rate_increase = d.pop("max_error_rate_increase")

        def _parse_max_latency_increase_percent(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        max_latency_increase_percent = _parse_max_latency_increase_percent(d.pop("max_latency_increase_percent"))

        min_calls = d.pop("min_calls")

        window_minutes = d.pop("window_minutes")

        state = check_outbound_ai_route_canary_state(d.pop("state"))

        def _parse_reason(data: object) -> None | OutboundAIRouteCanaryReasonType0:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                reason_type_0 = check_outbound_ai_route_canary_reason_type_0(data)

                return reason_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | OutboundAIRouteCanaryReasonType0, data)

        reason = _parse_reason(d.pop("reason"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        def _parse_decided_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                decided_at_type_0 = datetime.datetime.fromisoformat(data)

                return decided_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        decided_at = _parse_decided_at(d.pop("decided_at"))

        outbound_ai_route_canary = cls(
            target=target,
            percent=percent,
            max_error_rate_increase=max_error_rate_increase,
            max_latency_increase_percent=max_latency_increase_percent,
            min_calls=min_calls,
            window_minutes=window_minutes,
            state=state,
            reason=reason,
            started_at=started_at,
            decided_at=decided_at,
        )

        outbound_ai_route_canary.additional_properties = d
        return outbound_ai_route_canary

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
