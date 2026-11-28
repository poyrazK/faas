from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.financial_budget_revision_response import FinancialBudgetRevisionResponse


T = TypeVar("T", bound="FinancialBudgetHistoryResponse")


@_attrs_define
class FinancialBudgetHistoryResponse:
    """Immutable policy revision page and an optional exclusive continuation cursor."""

    revisions: list[FinancialBudgetRevisionResponse]
    next_revision: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revisions = []
        for revisions_item_data in self.revisions:
            revisions_item = revisions_item_data.to_dict()
            revisions.append(revisions_item)

        next_revision = self.next_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revisions": revisions,
            }
        )
        if next_revision is not UNSET:
            field_dict["next_revision"] = next_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_revision_response import FinancialBudgetRevisionResponse

        d = dict(src_dict)
        revisions = []
        _revisions = d.pop("revisions")
        for revisions_item_data in _revisions:
            revisions_item = FinancialBudgetRevisionResponse.from_dict(revisions_item_data)

            revisions.append(revisions_item)

        next_revision = d.pop("next_revision", UNSET)

        financial_budget_history_response = cls(
            revisions=revisions,
            next_revision=next_revision,
        )

        financial_budget_history_response.additional_properties = d
        return financial_budget_history_response

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
