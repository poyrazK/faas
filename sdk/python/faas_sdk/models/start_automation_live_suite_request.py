from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="StartAutomationLiveSuiteRequest")


@_attrs_define
class StartAutomationLiveSuiteRequest:
    """At most 16 KiB. Bind an ordered selection of saved live case names to the exact saved version, published revision
    and deployment, with explicit real-action authorization.

    """

    expected_version: int
    expected_published_version: int
    expected_deployment_id: UUID
    cases: list[str]
    confirm_real_actions: bool

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        expected_published_version = self.expected_published_version

        expected_deployment_id = str(self.expected_deployment_id)

        cases = self.cases

        confirm_real_actions = self.confirm_real_actions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "expected_published_version": expected_published_version,
                "expected_deployment_id": expected_deployment_id,
                "cases": cases,
                "confirm_real_actions": confirm_real_actions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        expected_published_version = d.pop("expected_published_version")

        expected_deployment_id = UUID(d.pop("expected_deployment_id"))

        cases = cast(list[str], d.pop("cases"))

        confirm_real_actions = d.pop("confirm_real_actions")

        start_automation_live_suite_request = cls(
            expected_version=expected_version,
            expected_published_version=expected_published_version,
            expected_deployment_id=expected_deployment_id,
            cases=cases,
            confirm_real_actions=confirm_real_actions,
        )

        return start_automation_live_suite_request
