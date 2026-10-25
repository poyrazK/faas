from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_live_suite_member import AutomationLiveSuiteMember


T = TypeVar("T", bound="SaveAutomationLiveSuiteRequest")


@_attrs_define
class SaveAutomationLiveSuiteRequest:
    """At most 16 KiB. Freeze an ordered saved-case selection and attach existing verification run IDs. No assertions,
    verdicts, keys or payloads are accepted. Saving never executes or checks a run. Existing run links cannot be
    replaced or removed.

    """

    expected_version: int
    expected_published_version: int
    expected_deployment_id: UUID
    cases: list[AutomationLiveSuiteMember]

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        expected_published_version = self.expected_published_version

        expected_deployment_id = str(self.expected_deployment_id)

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "expected_published_version": expected_published_version,
                "expected_deployment_id": expected_deployment_id,
                "cases": cases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_suite_member import AutomationLiveSuiteMember

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        expected_published_version = d.pop("expected_published_version")

        expected_deployment_id = UUID(d.pop("expected_deployment_id"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = AutomationLiveSuiteMember.from_dict(cases_item_data)

            cases.append(cases_item)

        save_automation_live_suite_request = cls(
            expected_version=expected_version,
            expected_published_version=expected_published_version,
            expected_deployment_id=expected_deployment_id,
            cases=cases,
        )

        return save_automation_live_suite_request
