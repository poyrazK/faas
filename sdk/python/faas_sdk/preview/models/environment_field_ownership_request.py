from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.environment_field_ownership_request_manager import (
    EnvironmentFieldOwnershipRequestManager,
    check_environment_field_ownership_request_manager,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentFieldOwnershipRequest")


@_attrs_define
class EnvironmentFieldOwnershipRequest:
    """Specify exactly one of app or project. App fields use variables/KEY or source; project fields use configuration/KEY.
    Terraform must claim before writes and release after deletion. Claims contain no values.

    """

    environment: str
    paths: list[str]
    manager: EnvironmentFieldOwnershipRequestManager
    app: str | Unset = UNSET
    project: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        environment = self.environment

        paths = self.paths

        manager: str = self.manager

        app = self.app

        project = self.project

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "environment": environment,
                "paths": paths,
                "manager": manager,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if project is not UNSET:
            field_dict["project"] = project

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        environment = d.pop("environment")

        paths = cast(list[str], d.pop("paths"))

        manager = check_environment_field_ownership_request_manager(d.pop("manager"))

        app = d.pop("app", UNSET)

        project = d.pop("project", UNSET)

        environment_field_ownership_request = cls(
            environment=environment,
            paths=paths,
            manager=manager,
            app=app,
            project=project,
        )

        return environment_field_ownership_request
