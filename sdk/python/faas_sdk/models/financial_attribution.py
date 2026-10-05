from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="FinancialAttribution")


@_attrs_define
class FinancialAttribution:
    """Workload identity retained at first observation, surviving rename and deletion."""

    app_id: str | Unset = UNSET
    job_id: str | Unset = UNSET
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    deployment_id: str | Unset = UNSET
    name: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        job_id = self.job_id

        project_id = self.project_id

        environment_id = self.environment_id

        deployment_id = self.deployment_id

        name = self.name

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if name is not UNSET:
            field_dict["name"] = name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        deployment_id = d.pop("deployment_id", UNSET)

        name = d.pop("name", UNSET)

        financial_attribution = cls(
            app_id=app_id,
            job_id=job_id,
            project_id=project_id,
            environment_id=environment_id,
            deployment_id=deployment_id,
            name=name,
        )

        financial_attribution.additional_properties = d
        return financial_attribution

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
