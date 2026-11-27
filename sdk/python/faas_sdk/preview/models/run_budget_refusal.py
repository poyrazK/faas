from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.run_budget_kind import RunBudgetKind, check_run_budget_kind
from ..models.run_budget_refusal_metric import RunBudgetRefusalMetric, check_run_budget_refusal_metric

T = TypeVar("T", bound="RunBudgetRefusal")


@_attrs_define
class RunBudgetRefusal:
    """Budget limit that prevented admission and the current usage against it."""

    kind: RunBudgetKind
    """Whether a budget applies to one customer or a named task."""
    ref: str
    metric: RunBudgetRefusalMetric
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        ref = self.ref

        metric: str = self.metric

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "ref": ref,
                "metric": metric,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_run_budget_kind(d.pop("kind"))

        ref = d.pop("ref")

        metric = check_run_budget_refusal_metric(d.pop("metric"))

        run_budget_refusal = cls(
            kind=kind,
            ref=ref,
            metric=metric,
        )

        run_budget_refusal.additional_properties = d
        return run_budget_refusal

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
