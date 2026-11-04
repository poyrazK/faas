from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingVerificationPlanStep")


@_attrs_define
class BindingVerificationPlanStep:
    """Selected binding resource and immutable operation planned for a verification receipt."""

    type_: str
    name: str
    binding: str
    scope: str
    action: str
    target_deployment_id: UUID | Unset = UNSET
    queue_environment: str | Unset = UNSET
    reason: str | Unset = UNSET
    required_evidence: str | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    prefix: str | Unset = UNSET
    required_mode: str | Unset = UNSET
    probe_mode: str | Unset = UNSET
    operations: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        name = self.name

        binding = self.binding

        scope = self.scope

        action = self.action

        target_deployment_id: str | Unset = UNSET
        if not isinstance(self.target_deployment_id, Unset):
            target_deployment_id = str(self.target_deployment_id)

        queue_environment = self.queue_environment

        reason = self.reason

        required_evidence = self.required_evidence

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        prefix = self.prefix

        required_mode = self.required_mode

        probe_mode = self.probe_mode

        operations: list[str] | Unset = UNSET
        if not isinstance(self.operations, Unset):
            operations = self.operations

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "binding": binding,
                "scope": scope,
                "action": action,
            }
        )
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if queue_environment is not UNSET:
            field_dict["queue_environment"] = queue_environment
        if reason is not UNSET:
            field_dict["reason"] = reason
        if required_evidence is not UNSET:
            field_dict["required_evidence"] = required_evidence
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if required_mode is not UNSET:
            field_dict["required_mode"] = required_mode
        if probe_mode is not UNSET:
            field_dict["probe_mode"] = probe_mode
        if operations is not UNSET:
            field_dict["operations"] = operations

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = d.pop("type")

        name = d.pop("name")

        binding = d.pop("binding")

        scope = d.pop("scope")

        action = d.pop("action")

        _target_deployment_id = d.pop("target_deployment_id", UNSET)
        target_deployment_id: UUID | Unset
        if isinstance(_target_deployment_id, Unset):
            target_deployment_id = UNSET
        else:
            target_deployment_id = UUID(_target_deployment_id)

        queue_environment = d.pop("queue_environment", UNSET)

        reason = d.pop("reason", UNSET)

        required_evidence = d.pop("required_evidence", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        prefix = d.pop("prefix", UNSET)

        required_mode = d.pop("required_mode", UNSET)

        probe_mode = d.pop("probe_mode", UNSET)

        operations = cast(list[str], d.pop("operations", UNSET))

        binding_verification_plan_step = cls(
            type_=type_,
            name=name,
            binding=binding,
            scope=scope,
            action=action,
            target_deployment_id=target_deployment_id,
            queue_environment=queue_environment,
            reason=reason,
            required_evidence=required_evidence,
            checked_at=checked_at,
            prefix=prefix,
            required_mode=required_mode,
            probe_mode=probe_mode,
            operations=operations,
        )

        binding_verification_plan_step.additional_properties = d
        return binding_verification_plan_step

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
