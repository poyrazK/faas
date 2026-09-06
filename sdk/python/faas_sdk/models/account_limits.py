from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_limits_plan import AccountLimitsPlan, check_account_limits_plan

T = TypeVar("T", bound="AccountLimits")


@_attrs_define
class AccountLimits:
    """Plan-driven quota and resource caps: max RAM per app, concurrent wakes, total deployed apps, included GB-hours, and
    writable ephemeral app-disk capacity.

    """

    plan: AccountLimitsPlan
    ram_mb: int
    max_concurrency: int
    deployed_apps: int
    included_gb_hours: int
    app_layer_max_mb: int
    ephemeral_disk_max_mb: int
    """Maximum writable ephemeral app-disk capacity per app, in MB. This is the same physical drive1 cap
    historically named app_layer_max_mb."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan: str = self.plan

        ram_mb = self.ram_mb

        max_concurrency = self.max_concurrency

        deployed_apps = self.deployed_apps

        included_gb_hours = self.included_gb_hours

        app_layer_max_mb = self.app_layer_max_mb

        ephemeral_disk_max_mb = self.ephemeral_disk_max_mb

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "ram_mb": ram_mb,
                "max_concurrency": max_concurrency,
                "deployed_apps": deployed_apps,
                "included_gb_hours": included_gb_hours,
                "app_layer_max_mb": app_layer_max_mb,
                "ephemeral_disk_max_mb": ephemeral_disk_max_mb,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan = check_account_limits_plan(d.pop("plan"))

        ram_mb = d.pop("ram_mb")

        max_concurrency = d.pop("max_concurrency")

        deployed_apps = d.pop("deployed_apps")

        included_gb_hours = d.pop("included_gb_hours")

        app_layer_max_mb = d.pop("app_layer_max_mb")

        ephemeral_disk_max_mb = d.pop("ephemeral_disk_max_mb")

        account_limits = cls(
            plan=plan,
            ram_mb=ram_mb,
            max_concurrency=max_concurrency,
            deployed_apps=deployed_apps,
            included_gb_hours=included_gb_hours,
            app_layer_max_mb=app_layer_max_mb,
            ephemeral_disk_max_mb=ephemeral_disk_max_mb,
        )

        account_limits.additional_properties = d
        return account_limits

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
