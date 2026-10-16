from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_git_ops_diagnostic_actor import (
    EnvironmentGitOpsDiagnosticActor,
    check_environment_git_ops_diagnostic_actor,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentGitOpsDiagnostic")


@_attrs_define
class EnvironmentGitOpsDiagnostic:
    """A recognized blocker with a responsible actor and a safe next action. Unrecognized upstream errors are redacted."""

    stage: str
    code: str
    message: str
    actor: EnvironmentGitOpsDiagnosticActor
    action: str
    resource: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        stage = self.stage

        code = self.code

        message = self.message

        actor: str = self.actor

        action = self.action

        resource = self.resource

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "stage": stage,
                "code": code,
                "message": message,
                "actor": actor,
                "action": action,
            }
        )
        if resource is not UNSET:
            field_dict["resource"] = resource

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        stage = d.pop("stage")

        code = d.pop("code")

        message = d.pop("message")

        actor = check_environment_git_ops_diagnostic_actor(d.pop("actor"))

        action = d.pop("action")

        resource = d.pop("resource", UNSET)

        environment_git_ops_diagnostic = cls(
            stage=stage,
            code=code,
            message=message,
            actor=actor,
            action=action,
            resource=resource,
        )

        environment_git_ops_diagnostic.additional_properties = d
        return environment_git_ops_diagnostic

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
