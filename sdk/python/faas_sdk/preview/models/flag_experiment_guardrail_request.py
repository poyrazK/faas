from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagExperimentGuardrailRequest")


@_attrs_define
class FlagExperimentGuardrailRequest:
    """Turns one guardrail off or sets its tolerance."""

    disabled: bool | Unset = UNSET
    """Turn the guardrail off."""
    max_increase: float | Unset = UNSET
    """Absolute increase over control that breaches the guardrail."""

    def to_dict(self) -> dict[str, Any]:
        disabled = self.disabled

        max_increase = self.max_increase

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if disabled is not UNSET:
            field_dict["disabled"] = disabled
        if max_increase is not UNSET:
            field_dict["max_increase"] = max_increase

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        disabled = d.pop("disabled", UNSET)

        max_increase = d.pop("max_increase", UNSET)

        flag_experiment_guardrail_request = cls(
            disabled=disabled,
            max_increase=max_increase,
        )

        return flag_experiment_guardrail_request
