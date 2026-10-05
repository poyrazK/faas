from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.app_health_check_action import AppHealthCheckAction, check_app_health_check_action
from ..models.app_health_check_code import AppHealthCheckCode, check_app_health_check_code
from ..models.app_health_check_reason import AppHealthCheckReason, check_app_health_check_reason
from ..models.app_health_check_status import AppHealthCheckStatus, check_app_health_check_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_health_finding import AppHealthFinding


T = TypeVar("T", bound="AppHealthCheck")


@_attrs_define
class AppHealthCheck:
    """Safe evidence explanation and an optional inspection destination."""

    code: AppHealthCheckCode
    status: AppHealthCheckStatus
    detail: str
    """Safe explanation without raw backend errors or probe output."""
    reason: AppHealthCheckReason | Unset = UNSET
    """Stable diagnostic reason, when available."""
    action: AppHealthCheckAction | Unset = UNSET
    deployment_id: str | Unset = UNSET
    findings: list[AppHealthFinding] | Unset = UNSET
    """Independent replica readiness failures and missing evidence, with failures first and stable target order
    within severity."""
    findings_truncated: bool | Unset = UNSET
    """Additional findings were omitted; capacity counts still cover the complete bounded instance scan."""

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        status: str = self.status

        detail = self.detail

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        deployment_id = self.deployment_id

        findings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.findings, Unset):
            findings = []
            for findings_item_data in self.findings:
                findings_item = findings_item_data.to_dict()
                findings.append(findings_item)

        findings_truncated = self.findings_truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "status": status,
                "detail": detail,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if action is not UNSET:
            field_dict["action"] = action
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if findings is not UNSET:
            field_dict["findings"] = findings
        if findings_truncated is not UNSET:
            field_dict["findings_truncated"] = findings_truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_health_finding import AppHealthFinding

        d = dict(src_dict)
        code = check_app_health_check_code(d.pop("code"))

        status = check_app_health_check_status(d.pop("status"))

        detail = d.pop("detail")

        _reason = d.pop("reason", UNSET)
        reason: AppHealthCheckReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_app_health_check_reason(_reason)

        _action = d.pop("action", UNSET)
        action: AppHealthCheckAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_app_health_check_action(_action)

        deployment_id = d.pop("deployment_id", UNSET)

        _findings = d.pop("findings", UNSET)
        findings: list[AppHealthFinding] | Unset = UNSET
        if _findings is not UNSET:
            findings = []
            for findings_item_data in _findings:
                findings_item = AppHealthFinding.from_dict(findings_item_data)

                findings.append(findings_item)

        findings_truncated = d.pop("findings_truncated", UNSET)

        app_health_check = cls(
            code=code,
            status=status,
            detail=detail,
            reason=reason,
            action=action,
            deployment_id=deployment_id,
            findings=findings,
            findings_truncated=findings_truncated,
        )

        return app_health_check
