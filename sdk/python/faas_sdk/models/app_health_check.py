from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.app_health_check_action import AppHealthCheckAction, check_app_health_check_action
from ..models.app_health_check_code import AppHealthCheckCode, check_app_health_check_code
from ..models.app_health_check_status import AppHealthCheckStatus, check_app_health_check_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppHealthCheck")


@_attrs_define
class AppHealthCheck:
    """Safe evidence explanation and an optional inspection destination."""

    code: AppHealthCheckCode
    status: AppHealthCheckStatus
    detail: str
    """Safe explanation without raw backend errors or probe output."""
    action: AppHealthCheckAction | Unset = UNSET
    deployment_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        status: str = self.status

        detail = self.detail

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        deployment_id = self.deployment_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "status": status,
                "detail": detail,
            }
        )
        if action is not UNSET:
            field_dict["action"] = action
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = check_app_health_check_code(d.pop("code"))

        status = check_app_health_check_status(d.pop("status"))

        detail = d.pop("detail")

        _action = d.pop("action", UNSET)
        action: AppHealthCheckAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_app_health_check_action(_action)

        deployment_id = d.pop("deployment_id", UNSET)

        app_health_check = cls(
            code=code,
            status=status,
            detail=detail,
            action=action,
            deployment_id=deployment_id,
        )

        return app_health_check
