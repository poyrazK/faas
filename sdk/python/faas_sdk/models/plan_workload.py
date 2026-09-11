from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_workload_action import PlanWorkloadAction, check_plan_workload_action
from ..models.plan_workload_class import PlanWorkloadClass, check_plan_workload_class
from ..models.plan_workload_tier import PlanWorkloadTier, check_plan_workload_tier
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.plan_detected_by import PlanDetectedBy


T = TypeVar("T", bound="PlanWorkload")


@_attrs_define
class PlanWorkload:
    """One discovered unit of work. Mirrors reposcan.Workload."""

    name: str
    root_dir: str
    command: list[str]
    ports: list[int]
    dockerfile: str | Unset = UNSET
    class_: PlanWorkloadClass | Unset = UNSET
    schedule: str | Unset = UNSET
    """cron expression when declared (CronJob, render, serverless)"""
    env_keys: list[str] | Unset = UNSET
    """KEYS only — never values; spec §11 forbids logging secrets"""
    source: str | Unset = UNSET
    """detector provenance, e.g. compose.yaml: api"""
    tier: PlanWorkloadTier | Unset = UNSET
    action: PlanWorkloadAction | Unset = UNSET
    """ADR-124 blast-radius projection. create = workload is new to the account; update = existing app matches
    (root_dir, name)."""
    existing_app_id: str | Unset = UNSET
    """ADR-124: app row ID the update targets. Empty iff action == create."""
    detected_by: PlanDetectedBy | Unset = UNSET
    """Structured detection trace for one workload (issue #742).
    `source` on PlanWorkload carries the same provenance as free
    text ("compose.yaml: api"); this is the machine-readable form
    so a client can branch on the detector without parsing it.

    Additive and optional: absent on any response the server did
    not populate, so existing consumers are unaffected.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        root_dir = self.root_dir

        command = self.command

        ports = self.ports

        dockerfile = self.dockerfile

        class_: str | Unset = UNSET
        if not isinstance(self.class_, Unset):
            class_ = self.class_

        schedule = self.schedule

        env_keys: list[str] | Unset = UNSET
        if not isinstance(self.env_keys, Unset):
            env_keys = self.env_keys

        source = self.source

        tier: str | Unset = UNSET
        if not isinstance(self.tier, Unset):
            tier = self.tier

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        existing_app_id = self.existing_app_id

        detected_by: dict[str, Any] | Unset = UNSET
        if not isinstance(self.detected_by, Unset):
            detected_by = self.detected_by.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "root_dir": root_dir,
                "command": command,
                "ports": ports,
            }
        )
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if class_ is not UNSET:
            field_dict["class"] = class_
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if env_keys is not UNSET:
            field_dict["env_keys"] = env_keys
        if source is not UNSET:
            field_dict["source"] = source
        if tier is not UNSET:
            field_dict["tier"] = tier
        if action is not UNSET:
            field_dict["action"] = action
        if existing_app_id is not UNSET:
            field_dict["existing_app_id"] = existing_app_id
        if detected_by is not UNSET:
            field_dict["detected_by"] = detected_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.plan_detected_by import PlanDetectedBy

        d = dict(src_dict)
        name = d.pop("name")

        root_dir = d.pop("root_dir")

        command = cast(list[str], d.pop("command"))

        ports = cast(list[int], d.pop("ports"))

        dockerfile = d.pop("dockerfile", UNSET)

        _class_ = d.pop("class", UNSET)
        class_: PlanWorkloadClass | Unset
        if isinstance(_class_, Unset):
            class_ = UNSET
        else:
            class_ = check_plan_workload_class(_class_)

        schedule = d.pop("schedule", UNSET)

        env_keys = cast(list[str], d.pop("env_keys", UNSET))

        source = d.pop("source", UNSET)

        _tier = d.pop("tier", UNSET)
        tier: PlanWorkloadTier | Unset
        if isinstance(_tier, Unset):
            tier = UNSET
        else:
            tier = check_plan_workload_tier(_tier)

        _action = d.pop("action", UNSET)
        action: PlanWorkloadAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_plan_workload_action(_action)

        existing_app_id = d.pop("existing_app_id", UNSET)

        _detected_by = d.pop("detected_by", UNSET)
        detected_by: PlanDetectedBy | Unset
        if isinstance(_detected_by, Unset):
            detected_by = UNSET
        else:
            detected_by = PlanDetectedBy.from_dict(_detected_by)

        plan_workload = cls(
            name=name,
            root_dir=root_dir,
            command=command,
            ports=ports,
            dockerfile=dockerfile,
            class_=class_,
            schedule=schedule,
            env_keys=env_keys,
            source=source,
            tier=tier,
            action=action,
            existing_app_id=existing_app_id,
            detected_by=detected_by,
        )

        plan_workload.additional_properties = d
        return plan_workload

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
