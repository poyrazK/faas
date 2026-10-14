from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_initial_failures_coverage import (
    WorkflowInitialFailuresCoverage,
    check_workflow_initial_failures_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_initial_failure import WorkflowInitialFailure


T = TypeVar("T", bound="WorkflowInitialFailures")


@_attrs_define
class WorkflowInitialFailures:
    """Page of captured initial workflow failures."""

    observed_at: datetime.datetime
    coverage: WorkflowInitialFailuresCoverage
    items: list[WorkflowInitialFailure]
    next_after: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "coverage": coverage,
                "items": items,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_initial_failure import WorkflowInitialFailure

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_workflow_initial_failures_coverage(d.pop("coverage"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = WorkflowInitialFailure.from_dict(items_item_data)

            items.append(items_item)

        next_after = d.pop("next_after", UNSET)

        workflow_initial_failures = cls(
            observed_at=observed_at,
            coverage=coverage,
            items=items,
            next_after=next_after,
        )

        return workflow_initial_failures
