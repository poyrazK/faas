from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.data_pipeline_quality_rule import DataPipelineQualityRule


T = TypeVar("T", bound="DataPipelineQualitySchedule")


@_attrs_define
class DataPipelineQualitySchedule:
    """Scheduled curated mirror checks, limited to 16 named rules and 10000 inspected source rows per rule. Incomplete
    coverage cannot be reported as passed; only counts and status leave the private worker.

    """

    every_seconds: int
    rules: list[DataPipelineQualityRule]

    def to_dict(self) -> dict[str, Any]:
        every_seconds = self.every_seconds

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "every_seconds": every_seconds,
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_quality_rule import DataPipelineQualityRule

        d = dict(src_dict)
        every_seconds = d.pop("every_seconds")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = DataPipelineQualityRule.from_dict(rules_item_data)

            rules.append(rules_item)

        data_pipeline_quality_schedule = cls(
            every_seconds=every_seconds,
            rules=rules,
        )

        return data_pipeline_quality_schedule
