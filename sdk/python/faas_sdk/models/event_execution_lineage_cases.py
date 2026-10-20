from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_case import EventExecutionLineageCase


T = TypeVar("T", bound="EventExecutionLineageCases")


@_attrs_define
class EventExecutionLineageCases:
    """Scoped page of retained execution-lineage diagnostics with a continuation boundary."""

    cases: list[EventExecutionLineageCase]
    next_after: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cases": cases,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_case import EventExecutionLineageCase

        d = dict(src_dict)
        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = EventExecutionLineageCase.from_dict(cases_item_data)

            cases.append(cases_item)

        next_after = d.pop("next_after", UNSET)

        event_execution_lineage_cases = cls(
            cases=cases,
            next_after=next_after,
        )

        return event_execution_lineage_cases
