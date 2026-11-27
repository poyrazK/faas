from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.insight_cohort import InsightCohort


T = TypeVar("T", bound="InsightCohortList")


@_attrs_define
class InsightCohortList:
    """An environment's cohorts."""

    cohorts: list[InsightCohort]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cohorts = []
        for cohorts_item_data in self.cohorts:
            cohorts_item = cohorts_item_data.to_dict()
            cohorts.append(cohorts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cohorts": cohorts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_cohort import InsightCohort

        d = dict(src_dict)
        cohorts = []
        _cohorts = d.pop("cohorts")
        for cohorts_item_data in _cohorts:
            cohorts_item = InsightCohort.from_dict(cohorts_item_data)

            cohorts.append(cohorts_item)

        insight_cohort_list = cls(
            cohorts=cohorts,
        )

        insight_cohort_list.additional_properties = d
        return insight_cohort_list

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
