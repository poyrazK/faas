from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tracing_forward import TracingForward


T = TypeVar("T", bound="TracingConfig")


@_attrs_define
class TracingConfig:
    """Opt-in zero-config request tracing baked into each deployment. Managed runtimes export database, cache and HTTP
    client spans to the debugger without an API key or code changes; apps that configure their own OTel exporter are
    left untouched.

    """

    enabled: bool
    sample_ratio: float | Unset = 1.0
    """Fraction of requests whose spans the app exports."""
    forward: TracingForward | Unset = UNSET
    """Sends a copy of every OpenTelemetry trace and log export the app makes to your own OTLP/HTTP endpoint, from
    inside the app's instance. Requires tracing to be enabled."""
    metrics: bool | Unset = False
    """Sends the app's OpenTelemetry metrics to Gregale from inside its instance, stored as its custom metrics (no
    API key needed). Requires tracing to be enabled and a plan with custom metrics. The managed auto-
    instrumentation's own metrics count toward the app's metric limit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        sample_ratio = self.sample_ratio

        forward: dict[str, Any] | Unset = UNSET
        if not isinstance(self.forward, Unset):
            forward = self.forward.to_dict()

        metrics = self.metrics

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if sample_ratio is not UNSET:
            field_dict["sample_ratio"] = sample_ratio
        if forward is not UNSET:
            field_dict["forward"] = forward
        if metrics is not UNSET:
            field_dict["metrics"] = metrics

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tracing_forward import TracingForward

        d = dict(src_dict)
        enabled = d.pop("enabled")

        sample_ratio = d.pop("sample_ratio", UNSET)

        _forward = d.pop("forward", UNSET)
        forward: TracingForward | Unset
        if isinstance(_forward, Unset):
            forward = UNSET
        else:
            forward = TracingForward.from_dict(_forward)

        metrics = d.pop("metrics", UNSET)

        tracing_config = cls(
            enabled=enabled,
            sample_ratio=sample_ratio,
            forward=forward,
            metrics=metrics,
        )

        tracing_config.additional_properties = d
        return tracing_config

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
