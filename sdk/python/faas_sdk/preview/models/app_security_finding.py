from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_security_finding_severity import AppSecurityFindingSeverity, check_app_security_finding_severity

T = TypeVar("T", bound="AppSecurityFinding")


@_attrs_define
class AppSecurityFinding:
    """One actionable security posture finding. Codes beginning with image_scan_ describe live-image scan-evidence
    coverage.

    """

    code: str
    severity: AppSecurityFindingSeverity
    title: str
    detail: str
    remediation: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        severity: str = self.severity

        title = self.title

        detail = self.detail

        remediation = self.remediation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "severity": severity,
                "title": title,
                "detail": detail,
                "remediation": remediation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        severity = check_app_security_finding_severity(d.pop("severity"))

        title = d.pop("title")

        detail = d.pop("detail")

        remediation = d.pop("remediation")

        app_security_finding = cls(
            code=code,
            severity=severity,
            title=title,
            detail=detail,
            remediation=remediation,
        )

        app_security_finding.additional_properties = d
        return app_security_finding

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
