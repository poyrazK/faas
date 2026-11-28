from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlanCron")


@_attrs_define
class PlanCron:
    """A cron expression lifted from a workload (k8s CronJob, render.yaml, serverless.yml)."""

    workload_name: str
    schedule: str
    path: str
    enabled: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload_name = self.workload_name

        schedule = self.schedule

        path = self.path

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload_name": workload_name,
                "schedule": schedule,
                "path": path,
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload_name = d.pop("workload_name")

        schedule = d.pop("schedule")

        path = d.pop("path")

        enabled = d.pop("enabled")

        plan_cron = cls(
            workload_name=workload_name,
            schedule=schedule,
            path=path,
            enabled=enabled,
        )

        plan_cron.additional_properties = d
        return plan_cron

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
