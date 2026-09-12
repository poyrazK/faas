from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_limits_plan import AccountLimitsPlan, check_account_limits_plan
from ..models.trigger_kind import TriggerKind, check_trigger_kind

T = TypeVar("T", bound="AccountLimits")


@_attrs_define
class AccountLimits:
    """Plan-driven quota, resource caps, and trigger capabilities returned by GET /v1/account."""

    plan: AccountLimitsPlan
    ram_mb: int
    vcpu: int
    """Plan-derived guest vCPU topology. Free/Hobby/Pro use 2; Scale uses 4. This is informational on account
    reads."""
    max_concurrency: int
    deployed_apps: int
    deploys_per_hour: int
    """Account-wide deployment admissions per fixed one-hour window."""
    developer_apps: int
    """Maximum live `gregale dev` environments for this plan."""
    included_gb_hours: int
    app_layer_max_mb: int
    ephemeral_disk_max_mb: int
    """Maximum writable ephemeral app-disk capacity per app, in MB. This is the same physical drive1 cap
    historically named app_layer_max_mb."""
    triggers_allowed: bool
    """Whether the plan permits external event triggers."""
    trigger_kinds: list[TriggerKind]
    """External trigger kinds this plan may create. Cron schedules use the dedicated crons API and are not
    included."""
    trigger_limit_per_app: int
    trigger_limit_per_account: int
    trigger_batch_size_max: int
    trigger_batch_window_max_ms: int
    """Maximum batching window in milliseconds."""
    trigger_max_attempts_max: int
    trigger_payload_max_bytes: int
    trigger_tls_skip_verify_allowed: bool
    """Whether Kafka tls.skip_verify=true is permitted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan: str = self.plan

        ram_mb = self.ram_mb

        vcpu = self.vcpu

        max_concurrency = self.max_concurrency

        deployed_apps = self.deployed_apps

        deploys_per_hour = self.deploys_per_hour

        developer_apps = self.developer_apps

        included_gb_hours = self.included_gb_hours

        app_layer_max_mb = self.app_layer_max_mb

        ephemeral_disk_max_mb = self.ephemeral_disk_max_mb

        triggers_allowed = self.triggers_allowed

        trigger_kinds = []
        for trigger_kinds_item_data in self.trigger_kinds:
            trigger_kinds_item: str = trigger_kinds_item_data
            trigger_kinds.append(trigger_kinds_item)

        trigger_limit_per_app = self.trigger_limit_per_app

        trigger_limit_per_account = self.trigger_limit_per_account

        trigger_batch_size_max = self.trigger_batch_size_max

        trigger_batch_window_max_ms = self.trigger_batch_window_max_ms

        trigger_max_attempts_max = self.trigger_max_attempts_max

        trigger_payload_max_bytes = self.trigger_payload_max_bytes

        trigger_tls_skip_verify_allowed = self.trigger_tls_skip_verify_allowed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "ram_mb": ram_mb,
                "vcpu": vcpu,
                "max_concurrency": max_concurrency,
                "deployed_apps": deployed_apps,
                "deploys_per_hour": deploys_per_hour,
                "developer_apps": developer_apps,
                "included_gb_hours": included_gb_hours,
                "app_layer_max_mb": app_layer_max_mb,
                "ephemeral_disk_max_mb": ephemeral_disk_max_mb,
                "triggers_allowed": triggers_allowed,
                "trigger_kinds": trigger_kinds,
                "trigger_limit_per_app": trigger_limit_per_app,
                "trigger_limit_per_account": trigger_limit_per_account,
                "trigger_batch_size_max": trigger_batch_size_max,
                "trigger_batch_window_max_ms": trigger_batch_window_max_ms,
                "trigger_max_attempts_max": trigger_max_attempts_max,
                "trigger_payload_max_bytes": trigger_payload_max_bytes,
                "trigger_tls_skip_verify_allowed": trigger_tls_skip_verify_allowed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan = check_account_limits_plan(d.pop("plan"))

        ram_mb = d.pop("ram_mb")

        vcpu = d.pop("vcpu")

        max_concurrency = d.pop("max_concurrency")

        deployed_apps = d.pop("deployed_apps")

        deploys_per_hour = d.pop("deploys_per_hour")

        developer_apps = d.pop("developer_apps")

        included_gb_hours = d.pop("included_gb_hours")

        app_layer_max_mb = d.pop("app_layer_max_mb")

        ephemeral_disk_max_mb = d.pop("ephemeral_disk_max_mb")

        triggers_allowed = d.pop("triggers_allowed")

        trigger_kinds = []
        _trigger_kinds = d.pop("trigger_kinds")
        for trigger_kinds_item_data in _trigger_kinds:
            trigger_kinds_item = check_trigger_kind(trigger_kinds_item_data)

            trigger_kinds.append(trigger_kinds_item)

        trigger_limit_per_app = d.pop("trigger_limit_per_app")

        trigger_limit_per_account = d.pop("trigger_limit_per_account")

        trigger_batch_size_max = d.pop("trigger_batch_size_max")

        trigger_batch_window_max_ms = d.pop("trigger_batch_window_max_ms")

        trigger_max_attempts_max = d.pop("trigger_max_attempts_max")

        trigger_payload_max_bytes = d.pop("trigger_payload_max_bytes")

        trigger_tls_skip_verify_allowed = d.pop("trigger_tls_skip_verify_allowed")

        account_limits = cls(
            plan=plan,
            ram_mb=ram_mb,
            vcpu=vcpu,
            max_concurrency=max_concurrency,
            deployed_apps=deployed_apps,
            deploys_per_hour=deploys_per_hour,
            developer_apps=developer_apps,
            included_gb_hours=included_gb_hours,
            app_layer_max_mb=app_layer_max_mb,
            ephemeral_disk_max_mb=ephemeral_disk_max_mb,
            triggers_allowed=triggers_allowed,
            trigger_kinds=trigger_kinds,
            trigger_limit_per_app=trigger_limit_per_app,
            trigger_limit_per_account=trigger_limit_per_account,
            trigger_batch_size_max=trigger_batch_size_max,
            trigger_batch_window_max_ms=trigger_batch_window_max_ms,
            trigger_max_attempts_max=trigger_max_attempts_max,
            trigger_payload_max_bytes=trigger_payload_max_bytes,
            trigger_tls_skip_verify_allowed=trigger_tls_skip_verify_allowed,
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
