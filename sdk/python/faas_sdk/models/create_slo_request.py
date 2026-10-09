from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_slo_request_latency_threshold_ms import (
    CreateSLORequestLatencyThresholdMs,
    check_create_slo_request_latency_threshold_ms,
)
from ..models.create_slo_request_sli import CreateSLORequestSli, check_create_slo_request_sli
from ..models.create_slo_request_window_days import CreateSLORequestWindowDays, check_create_slo_request_window_days
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateSLORequest")


@_attrs_define
class CreateSLORequest:
    """Defines a customer SLO on an app (ADR-747)."""

    name: str
    """Unique per app; lowercase letters, digits, dashes and underscores."""
    sli: CreateSLORequestSli
    """availability counts non-5xx responses among 2xx and 5xx; latency counts requests completing within
    latency_threshold_ms."""
    objective_pct: float
    """Target share of good requests, as a percentage with at most two decimals."""
    window_days: CreateSLORequestWindowDays
    """Rolling window over which the error budget is measured."""
    latency_threshold_ms: CreateSLORequestLatencyThresholdMs | Unset = UNSET
    """Required for the latency SLI and rejected otherwise; one of the gateway histogram bucket bounds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        sli: str = self.sli

        objective_pct = self.objective_pct

        window_days: int = self.window_days

        latency_threshold_ms: int | Unset = UNSET
        if not isinstance(self.latency_threshold_ms, Unset):
            latency_threshold_ms = self.latency_threshold_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "sli": sli,
                "objective_pct": objective_pct,
                "window_days": window_days,
            }
        )
        if latency_threshold_ms is not UNSET:
            field_dict["latency_threshold_ms"] = latency_threshold_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        sli = check_create_slo_request_sli(d.pop("sli"))

        objective_pct = d.pop("objective_pct")

        window_days = check_create_slo_request_window_days(d.pop("window_days"))

        _latency_threshold_ms = d.pop("latency_threshold_ms", UNSET)
        latency_threshold_ms: CreateSLORequestLatencyThresholdMs | Unset
        if isinstance(_latency_threshold_ms, Unset):
            latency_threshold_ms = UNSET
        else:
            latency_threshold_ms = check_create_slo_request_latency_threshold_ms(_latency_threshold_ms)

        create_slo_request = cls(
            name=name,
            sli=sli,
            objective_pct=objective_pct,
            window_days=window_days,
            latency_threshold_ms=latency_threshold_ms,
        )

        create_slo_request.additional_properties = d
        return create_slo_request

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
