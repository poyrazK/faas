from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APISandboxSuiteRunRequest")


@_attrs_define
class APISandboxSuiteRunRequest:
    """Saved suite revision and live baseline pinned for this run."""

    suite_revision: UUID
    baseline_deployment_id: UUID
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        suite_revision = str(self.suite_revision)

        baseline_deployment_id = str(self.baseline_deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "suite_revision": suite_revision,
                "baseline_deployment_id": baseline_deployment_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        suite_revision = UUID(d.pop("suite_revision"))

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        api_sandbox_suite_run_request = cls(
            suite_revision=suite_revision,
            baseline_deployment_id=baseline_deployment_id,
        )

        api_sandbox_suite_run_request.additional_properties = d
        return api_sandbox_suite_run_request

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
