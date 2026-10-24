from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AIDeploymentUsage")


@_attrs_define
class AIDeploymentUsage:
    """One deployment's AI gateway calls over the last 14 days (ADR 20261022153155)."""

    deployment_id: UUID
    first_call_at: datetime.datetime
    """The UTC minute of its first call in the window."""
    last_call_at: datetime.datetime
    """The UTC minute of its latest call."""
    calls: int
    error_calls: int
    """Calls the provider failed."""
    flagged_calls: int
    """Calls whose answer had a quality flag."""
    cost_millicents: int
    input_tokens: int
    output_tokens: int
    error_rate: float
    flagged_rate: float
    cost_per_call_millicents: int
    mean_ttfb_ms: int | Unset = UNSET
    """Mean time to first byte; omitted when no call had one."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        first_call_at = self.first_call_at.isoformat()

        last_call_at = self.last_call_at.isoformat()

        calls = self.calls

        error_calls = self.error_calls

        flagged_calls = self.flagged_calls

        cost_millicents = self.cost_millicents

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        error_rate = self.error_rate

        flagged_rate = self.flagged_rate

        cost_per_call_millicents = self.cost_per_call_millicents

        mean_ttfb_ms = self.mean_ttfb_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "first_call_at": first_call_at,
                "last_call_at": last_call_at,
                "calls": calls,
                "error_calls": error_calls,
                "flagged_calls": flagged_calls,
                "cost_millicents": cost_millicents,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "error_rate": error_rate,
                "flagged_rate": flagged_rate,
                "cost_per_call_millicents": cost_per_call_millicents,
            }
        )
        if mean_ttfb_ms is not UNSET:
            field_dict["mean_ttfb_ms"] = mean_ttfb_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        first_call_at = datetime.datetime.fromisoformat(d.pop("first_call_at"))

        last_call_at = datetime.datetime.fromisoformat(d.pop("last_call_at"))

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        flagged_calls = d.pop("flagged_calls")

        cost_millicents = d.pop("cost_millicents")

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        error_rate = d.pop("error_rate")

        flagged_rate = d.pop("flagged_rate")

        cost_per_call_millicents = d.pop("cost_per_call_millicents")

        mean_ttfb_ms = d.pop("mean_ttfb_ms", UNSET)

        ai_deployment_usage = cls(
            deployment_id=deployment_id,
            first_call_at=first_call_at,
            last_call_at=last_call_at,
            calls=calls,
            error_calls=error_calls,
            flagged_calls=flagged_calls,
            cost_millicents=cost_millicents,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            error_rate=error_rate,
            flagged_rate=flagged_rate,
            cost_per_call_millicents=cost_per_call_millicents,
            mean_ttfb_ms=mean_ttfb_ms,
        )

        ai_deployment_usage.additional_properties = d
        return ai_deployment_usage

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
