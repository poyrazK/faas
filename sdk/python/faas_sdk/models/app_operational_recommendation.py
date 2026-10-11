from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_operational_recommendation_severity import (
    AppOperationalRecommendationSeverity,
    check_app_operational_recommendation_severity,
)

T = TypeVar("T", bound="AppOperationalRecommendation")


@_attrs_define
class AppOperationalRecommendation:
    """An actionable explanation of current operational evidence and an existing command or status endpoint for
    investigation.

    """

    code: str
    severity: AppOperationalRecommendationSeverity
    message: str
    next_: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        severity: str = self.severity

        message = self.message

        next_ = self.next_

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "severity": severity,
                "message": message,
                "next": next_,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        severity = check_app_operational_recommendation_severity(d.pop("severity"))

        message = d.pop("message")

        next_ = d.pop("next")

        app_operational_recommendation = cls(
            code=code,
            severity=severity,
            message=message,
            next_=next_,
        )

        app_operational_recommendation.additional_properties = d
        return app_operational_recommendation

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
