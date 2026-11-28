from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_outcome_type import FlagOutcomeType, check_flag_outcome_type

T = TypeVar("T", bound="FlagOutcome")


@_attrs_define
class FlagOutcome:
    """Request-weighted observations for one boolean value or named variant. Boolean values are encoded as the strings
    "true" and "false".

    """

    type_: FlagOutcomeType
    value: str
    """Boolean value "true"/"false" or the selected variant key."""
    request_count: int
    used_count: int
    """Application-reported requests where the decision was marked used."""
    http_5xx_count: int
    http_5xx_rate: float
    """HTTP 5xx request count divided by request_count."""
    p50_latency_ms: int
    """Request-weighted nearest-rank p50 from stored latency bucket upper bounds."""
    p95_latency_ms: int
    """Request-weighted nearest-rank p95 from stored latency bucket upper bounds."""
    latency_quantized: bool
    """Latency values are conservative bucket upper bounds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        value = self.value

        request_count = self.request_count

        used_count = self.used_count

        http_5xx_count = self.http_5xx_count

        http_5xx_rate = self.http_5xx_rate

        p50_latency_ms = self.p50_latency_ms

        p95_latency_ms = self.p95_latency_ms

        latency_quantized = self.latency_quantized

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "value": value,
                "request_count": request_count,
                "used_count": used_count,
                "http_5xx_count": http_5xx_count,
                "http_5xx_rate": http_5xx_rate,
                "p50_latency_ms": p50_latency_ms,
                "p95_latency_ms": p95_latency_ms,
                "latency_quantized": latency_quantized,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_flag_outcome_type(d.pop("type"))

        value = d.pop("value")

        request_count = d.pop("request_count")

        used_count = d.pop("used_count")

        http_5xx_count = d.pop("http_5xx_count")

        http_5xx_rate = d.pop("http_5xx_rate")

        p50_latency_ms = d.pop("p50_latency_ms")

        p95_latency_ms = d.pop("p95_latency_ms")

        latency_quantized = d.pop("latency_quantized")

        flag_outcome = cls(
            type_=type_,
            value=value,
            request_count=request_count,
            used_count=used_count,
            http_5xx_count=http_5xx_count,
            http_5xx_rate=http_5xx_rate,
            p50_latency_ms=p50_latency_ms,
            p95_latency_ms=p95_latency_ms,
            latency_quantized=latency_quantized,
        )

        flag_outcome.additional_properties = d
        return flag_outcome

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
