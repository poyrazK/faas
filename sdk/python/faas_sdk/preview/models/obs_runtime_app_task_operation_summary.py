from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_operation_name import (
    ObsRuntimeAppTaskOperationName,
    check_obs_runtime_app_task_operation_name,
)

T = TypeVar("T", bound="ObsRuntimeAppTaskOperationSummary")


@_attrs_define
class ObsRuntimeAppTaskOperationSummary:
    """Coverage for one ordered operation across all inspected targets and seven outcomes. Category fields count signals
    with a positive observed delta, not delta magnitudes. Error returns, cancellation, lease loss and capacity refusal
    remain distinct. Execute success may include a customer command verdict and does not establish successful delivery
    or completion.

    """

    operation: ObsRuntimeAppTaskOperationName
    """Fixed operation calls included in this advisory comparison. Destroy measures cleanup calls, not confirmed VM
    absence; complete measures acknowledgements, not successful commands."""
    increased: int
    unchanged: int
    baseline: int
    unknown: int
    error_increases: int
    cancellations: int
    lease_losses: int
    capacity_refusals: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation: str = self.operation

        increased = self.increased

        unchanged = self.unchanged

        baseline = self.baseline

        unknown = self.unknown

        error_increases = self.error_increases

        cancellations = self.cancellations

        lease_losses = self.lease_losses

        capacity_refusals = self.capacity_refusals

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation": operation,
                "increased": increased,
                "unchanged": unchanged,
                "baseline": baseline,
                "unknown": unknown,
                "error_increases": error_increases,
                "cancellations": cancellations,
                "lease_losses": lease_losses,
                "capacity_refusals": capacity_refusals,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation = check_obs_runtime_app_task_operation_name(d.pop("operation"))

        increased = d.pop("increased")

        unchanged = d.pop("unchanged")

        baseline = d.pop("baseline")

        unknown = d.pop("unknown")

        error_increases = d.pop("error_increases")

        cancellations = d.pop("cancellations")

        lease_losses = d.pop("lease_losses")

        capacity_refusals = d.pop("capacity_refusals")

        obs_runtime_app_task_operation_summary = cls(
            operation=operation,
            increased=increased,
            unchanged=unchanged,
            baseline=baseline,
            unknown=unknown,
            error_increases=error_increases,
            cancellations=cancellations,
            lease_losses=lease_losses,
            capacity_refusals=capacity_refusals,
        )

        obs_runtime_app_task_operation_summary.additional_properties = d
        return obs_runtime_app_task_operation_summary

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
