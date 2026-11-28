from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.update_native_issue_tracker_status_request_desired_state import (
    UpdateNativeIssueTrackerStatusRequestDesiredState,
    check_update_native_issue_tracker_status_request_desired_state,
)

if TYPE_CHECKING:
    from ..models.issue_tracker_provider_status import IssueTrackerProviderStatus


T = TypeVar("T", bound="UpdateNativeIssueTrackerStatusRequest")


@_attrs_define
class UpdateNativeIssueTrackerStatusRequest:
    """Expected provider snapshot and current Gregale lifecycle to push through a native connection."""

    desired_state: UpdateNativeIssueTrackerStatusRequestDesiredState
    expected: IssueTrackerProviderStatus
    """Observed lifecycle and identity of an external tracker issue."""

    def to_dict(self) -> dict[str, Any]:
        desired_state: str = self.desired_state

        expected = self.expected.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "desired_state": desired_state,
                "expected": expected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_tracker_provider_status import IssueTrackerProviderStatus

        d = dict(src_dict)
        desired_state = check_update_native_issue_tracker_status_request_desired_state(d.pop("desired_state"))

        expected = IssueTrackerProviderStatus.from_dict(d.pop("expected"))

        update_native_issue_tracker_status_request = cls(
            desired_state=desired_state,
            expected=expected,
        )

        return update_native_issue_tracker_status_request
