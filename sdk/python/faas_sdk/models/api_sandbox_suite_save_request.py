from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxSuiteSaveRequest")


@_attrs_define
class APISandboxSuiteSaveRequest:
    expected_revision: str
    """Use the current suite revision or the empty string to create the suite."""
    scenario_ids: list[UUID]
    run_before_approval: bool | Unset = UNSET
    """Enable or disable automatic advisory suites. An omitted value keeps the existing setting and an empty
    scenario list disables it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        scenario_ids = []
        for scenario_ids_item_data in self.scenario_ids:
            scenario_ids_item = str(scenario_ids_item_data)
            scenario_ids.append(scenario_ids_item)

        run_before_approval = self.run_before_approval

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_revision": expected_revision,
                "scenario_ids": scenario_ids,
            }
        )
        if run_before_approval is not UNSET:
            field_dict["run_before_approval"] = run_before_approval

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        scenario_ids = []
        _scenario_ids = d.pop("scenario_ids")
        for scenario_ids_item_data in _scenario_ids:
            scenario_ids_item = UUID(scenario_ids_item_data)

            scenario_ids.append(scenario_ids_item)

        run_before_approval = d.pop("run_before_approval", UNSET)

        api_sandbox_suite_save_request = cls(
            expected_revision=expected_revision,
            scenario_ids=scenario_ids,
            run_before_approval=run_before_approval,
        )

        api_sandbox_suite_save_request.additional_properties = d
        return api_sandbox_suite_save_request

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
