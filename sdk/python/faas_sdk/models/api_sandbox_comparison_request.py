from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APISandboxComparisonRequest")


@_attrs_define
class APISandboxComparisonRequest:
    """Baseline release and scenario revision used for a two-release comparison."""

    baseline_deployment_id: UUID
    scenario_revision: UUID
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        baseline_deployment_id = str(self.baseline_deployment_id)

        scenario_revision = str(self.scenario_revision)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "baseline_deployment_id": baseline_deployment_id,
                "scenario_revision": scenario_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        scenario_revision = UUID(d.pop("scenario_revision"))

        api_sandbox_comparison_request = cls(
            baseline_deployment_id=baseline_deployment_id,
            scenario_revision=scenario_revision,
        )

        api_sandbox_comparison_request.additional_properties = d
        return api_sandbox_comparison_request

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
