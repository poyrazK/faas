from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.run_budget_limits import RunBudgetLimits


T = TypeVar("T", bound="SetRunBudgetRequest")


@_attrs_define
class SetRunBudgetRequest:
    """Replacement budget policy and the revision required to update it."""

    expected_revision: int
    enabled: bool
    limits: RunBudgetLimits
    """Optional concurrency, invocation and compute caps for a customer or task."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        enabled = self.enabled

        limits = self.limits.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_revision": expected_revision,
                "enabled": enabled,
                "limits": limits,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.run_budget_limits import RunBudgetLimits

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        enabled = d.pop("enabled")

        limits = RunBudgetLimits.from_dict(d.pop("limits"))

        set_run_budget_request = cls(
            expected_revision=expected_revision,
            enabled=enabled,
            limits=limits,
        )

        set_run_budget_request.additional_properties = d
        return set_run_budget_request

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
