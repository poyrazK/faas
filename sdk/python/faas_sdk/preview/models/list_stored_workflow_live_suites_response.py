from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.stored_workflow_live_suite_summary import StoredWorkflowLiveSuiteSummary


T = TypeVar("T", bound="ListStoredWorkflowLiveSuitesResponse")


@_attrs_define
class ListStoredWorkflowLiveSuitesResponse:
    """Twenty most recently created suites. One hundred recent suites are retained per stored workflow; clearing saved test
    data clears shared suite history.

    """

    suites: list[StoredWorkflowLiveSuiteSummary]

    def to_dict(self) -> dict[str, Any]:
        suites = []
        for suites_item_data in self.suites:
            suites_item = suites_item_data.to_dict()
            suites.append(suites_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "suites": suites,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.stored_workflow_live_suite_summary import StoredWorkflowLiveSuiteSummary

        d = dict(src_dict)
        suites = []
        _suites = d.pop("suites")
        for suites_item_data in _suites:
            suites_item = StoredWorkflowLiveSuiteSummary.from_dict(suites_item_data)

            suites.append(suites_item)

        list_stored_workflow_live_suites_response = cls(
            suites=suites,
        )

        return list_stored_workflow_live_suites_response
