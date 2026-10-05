from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_budget_spec import FinancialBudgetSpec


T = TypeVar("T", bound="FinancialBudgetPreviewRequest")


@_attrs_define
class FinancialBudgetPreviewRequest:
    """Proposed budget intent to inspect without saving or activating it."""

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

        financial_budget_preview_request = cls(
            spec=spec,
        )

        financial_budget_preview_request.additional_properties = d
        return financial_budget_preview_request

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
