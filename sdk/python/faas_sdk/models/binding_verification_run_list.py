from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_run_summary import BindingVerificationRunSummary


T = TypeVar("T", bound="BindingVerificationRunList")


@_attrs_define
class BindingVerificationRunList:
    """Bounded retained receipt summaries and a cursor tied to the original observation cutoff."""

    runs: list[BindingVerificationRunSummary]
    observed_at: datetime.datetime
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        runs = []
        for runs_item_data in self.runs:
            runs_item = runs_item_data.to_dict()
            runs.append(runs_item)

        observed_at = self.observed_at.isoformat()

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "runs": runs,
                "observed_at": observed_at,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_run_summary import BindingVerificationRunSummary

        d = dict(src_dict)
        runs = []
        _runs = d.pop("runs")
        for runs_item_data in _runs:
            runs_item = BindingVerificationRunSummary.from_dict(runs_item_data)

            runs.append(runs_item)

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        next_cursor = d.pop("next_cursor", UNSET)

        binding_verification_run_list = cls(
            runs=runs,
            observed_at=observed_at,
            next_cursor=next_cursor,
        )

        return binding_verification_run_list
