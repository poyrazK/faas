from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="StartAutomationLiveVerificationRequest")


@_attrs_define
class StartAutomationLiveVerificationRequest:
    """Fenced request to execute one saved case input against a specific published revision and deployment."""

    expected_version: int
    expected_published_version: int
    expected_deployment_id: str
    case_name: str
    confirm_real_actions: bool
    """Must be true to authorize real handler and connected-service actions."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        expected_published_version = self.expected_published_version

        expected_deployment_id = self.expected_deployment_id

        case_name = self.case_name

        confirm_real_actions = self.confirm_real_actions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "expected_published_version": expected_published_version,
                "expected_deployment_id": expected_deployment_id,
                "case_name": case_name,
                "confirm_real_actions": confirm_real_actions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        expected_published_version = d.pop("expected_published_version")

        expected_deployment_id = d.pop("expected_deployment_id")

        case_name = d.pop("case_name")

        confirm_real_actions = d.pop("confirm_real_actions")

        start_automation_live_verification_request = cls(
            expected_version=expected_version,
            expected_published_version=expected_published_version,
            expected_deployment_id=expected_deployment_id,
            case_name=case_name,
            confirm_real_actions=confirm_real_actions,
        )

        return start_automation_live_verification_request
