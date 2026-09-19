from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_security_posture_response_profile import (
    AppSecurityPostureResponseProfile,
    check_app_security_posture_response_profile,
)
from ..models.app_security_posture_response_security_policy import (
    AppSecurityPostureResponseSecurityPolicy,
    check_app_security_posture_response_security_policy,
)

if TYPE_CHECKING:
    from ..models.app_security_finding import AppSecurityFinding


T = TypeVar("T", bound="AppSecurityPostureResponse")


@_attrs_define
class AppSecurityPostureResponse:
    """Read-only deterministic configuration posture for an app."""

    app_id: str
    slug: str
    profile: AppSecurityPostureResponseProfile
    score: int
    security_policy: AppSecurityPostureResponseSecurityPolicy
    """The app's deploy-time response to high-severity posture findings."""
    findings: list[AppSecurityFinding]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        slug = self.slug

        profile: str = self.profile

        score = self.score

        security_policy: str = self.security_policy

        findings = []
        for findings_item_data in self.findings:
            findings_item = findings_item_data.to_dict()
            findings.append(findings_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "profile": profile,
                "score": score,
                "security_policy": security_policy,
                "findings": findings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_security_finding import AppSecurityFinding

        d = dict(src_dict)
        app_id = d.pop("app_id")

        slug = d.pop("slug")

        profile = check_app_security_posture_response_profile(d.pop("profile"))

        score = d.pop("score")

        security_policy = check_app_security_posture_response_security_policy(d.pop("security_policy"))

        findings = []
        _findings = d.pop("findings")
        for findings_item_data in _findings:
            findings_item = AppSecurityFinding.from_dict(findings_item_data)

            findings.append(findings_item)

        app_security_posture_response = cls(
            app_id=app_id,
            slug=slug,
            profile=profile,
            score=score,
            security_policy=security_policy,
            findings=findings,
        )

        app_security_posture_response.additional_properties = d
        return app_security_posture_response

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
