from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_budget_spec import FinancialBudgetSpec


T = TypeVar("T", bound="CreateFinancialBudgetRequest")


@_attrs_define
class CreateFinancialBudgetRequest:
    """New budget intent; activation supports monitored notify, monitored compute-only stop_previews, and strict
    suspend_workloads (app/job gross compute or account net compute optionally combined with egress) with zero drain and
    no alert thresholds. Strict activation requires quiet covered workloads, complete accounting, and native owners.
    Account strict activation also requires recorded allowances and native owners capable of funded jobs. Combining
    compute and egress additionally requires complete egress accounting and byte-capable native owners.

    """

    spec: FinancialBudgetSpec
    """Customer budget intent; activation and enforcement are separately acknowledged."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        spec = self.spec.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "spec": spec,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_spec import FinancialBudgetSpec

        d = dict(src_dict)
        spec = FinancialBudgetSpec.from_dict(d.pop("spec"))

        create_financial_budget_request = cls(
            spec=spec,
        )

        create_financial_budget_request.additional_properties = d
        return create_financial_budget_request

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
