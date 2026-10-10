from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_test_chaos_rule_direction import (
    ScenarioTestChaosRuleDirection,
    check_scenario_test_chaos_rule_direction,
)
from ..models.scenario_test_chaos_rule_kind import ScenarioTestChaosRuleKind, check_scenario_test_chaos_rule_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioTestChaosRule")


@_attrs_define
class ScenarioTestChaosRule:
    """One bounded HTTP request, TCP stream, or new TCP connection fault for matching internal scenario services."""

    to: str
    kind: ScenarioTestChaosRuleKind
    percent: int
    seed: int
    from_: str | Unset = UNSET
    latency_ms: int | Unset = UNSET
    status_code: int | Unset = UNSET
    port: int | Unset = UNSET
    """Required target port for TCP rules."""
    direction: ScenarioTestChaosRuleDirection | Unset = UNSET
    """TCP direction relative to the calling application. Defaults to both; connection faults require both."""
    rate_kib_per_second: int | Unset = UNSET
    """Required per-connection directional throughput cap for tcp_bandwidth."""
    reset_after_ms: int | Unset = UNSET
    """Delay before tcp_reset. Defaults to an immediate reset."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        to = self.to

        kind: str = self.kind

        percent = self.percent

        seed = self.seed

        from_ = self.from_

        latency_ms = self.latency_ms

        status_code = self.status_code

        port = self.port

        direction: str | Unset = UNSET
        if not isinstance(self.direction, Unset):
            direction = self.direction

        rate_kib_per_second = self.rate_kib_per_second

        reset_after_ms = self.reset_after_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "to": to,
                "kind": kind,
                "percent": percent,
                "seed": seed,
            }
        )
        if from_ is not UNSET:
            field_dict["from"] = from_
        if latency_ms is not UNSET:
            field_dict["latency_ms"] = latency_ms
        if status_code is not UNSET:
            field_dict["status_code"] = status_code
        if port is not UNSET:
            field_dict["port"] = port
        if direction is not UNSET:
            field_dict["direction"] = direction
        if rate_kib_per_second is not UNSET:
            field_dict["rate_kib_per_second"] = rate_kib_per_second
        if reset_after_ms is not UNSET:
            field_dict["reset_after_ms"] = reset_after_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        to = d.pop("to")

        kind = check_scenario_test_chaos_rule_kind(d.pop("kind"))

        percent = d.pop("percent")

        seed = d.pop("seed")

        from_ = d.pop("from", UNSET)

        latency_ms = d.pop("latency_ms", UNSET)

        status_code = d.pop("status_code", UNSET)

        port = d.pop("port", UNSET)

        _direction = d.pop("direction", UNSET)
        direction: ScenarioTestChaosRuleDirection | Unset
        if isinstance(_direction, Unset):
            direction = UNSET
        else:
            direction = check_scenario_test_chaos_rule_direction(_direction)

        rate_kib_per_second = d.pop("rate_kib_per_second", UNSET)

        reset_after_ms = d.pop("reset_after_ms", UNSET)

        scenario_test_chaos_rule = cls(
            to=to,
            kind=kind,
            percent=percent,
            seed=seed,
            from_=from_,
            latency_ms=latency_ms,
            status_code=status_code,
            port=port,
            direction=direction,
            rate_kib_per_second=rate_kib_per_second,
            reset_after_ms=reset_after_ms,
        )

        scenario_test_chaos_rule.additional_properties = d
        return scenario_test_chaos_rule

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
