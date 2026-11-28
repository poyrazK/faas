from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_experiment_guardrail_setting import FlagExperimentGuardrailSetting


T = TypeVar("T", bound="FlagExperimentGuardrailSettings")


@_attrs_define
class FlagExperimentGuardrailSettings:
    """An experiment's guardrails."""

    server_error_rate: FlagExperimentGuardrailSetting
    """One guardrail as configured."""
    p95_latency: FlagExperimentGuardrailSetting
    """One guardrail as configured."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        server_error_rate = self.server_error_rate.to_dict()

        p95_latency = self.p95_latency.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "server_error_rate": server_error_rate,
                "p95_latency": p95_latency,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_guardrail_setting import FlagExperimentGuardrailSetting

        d = dict(src_dict)
        server_error_rate = FlagExperimentGuardrailSetting.from_dict(d.pop("server_error_rate"))

        p95_latency = FlagExperimentGuardrailSetting.from_dict(d.pop("p95_latency"))

        flag_experiment_guardrail_settings = cls(
            server_error_rate=server_error_rate,
            p95_latency=p95_latency,
        )

        flag_experiment_guardrail_settings.additional_properties = d
        return flag_experiment_guardrail_settings

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
