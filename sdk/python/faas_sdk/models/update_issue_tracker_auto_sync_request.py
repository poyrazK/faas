from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.update_issue_tracker_auto_sync_request_expected_issue_state import (
    UpdateIssueTrackerAutoSyncRequestExpectedIssueState,
    check_update_issue_tracker_auto_sync_request_expected_issue_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.native_issue_tracker_status import NativeIssueTrackerStatus


T = TypeVar("T", bound="UpdateIssueTrackerAutoSyncRequest")


@_attrs_define
class UpdateIssueTrackerAutoSyncRequest:
    """Explicit automatic push delegation against a known policy revision and aligned native/source lifecycle snapshot."""

    enabled: bool
    expected_revision: int
    expected_status: NativeIssueTrackerStatus | Unset = UNSET
    """Verified native connection and observed status of a linked tracker issue."""
    expected_issue_state: UpdateIssueTrackerAutoSyncRequestExpectedIssueState | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        expected_revision = self.expected_revision

        expected_status: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_status, Unset):
            expected_status = self.expected_status.to_dict()

        expected_issue_state: str | Unset = UNSET
        if not isinstance(self.expected_issue_state, Unset):
            expected_issue_state = self.expected_issue_state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "expected_revision": expected_revision,
            }
        )
        if expected_status is not UNSET:
            field_dict["expected_status"] = expected_status
        if expected_issue_state is not UNSET:
            field_dict["expected_issue_state"] = expected_issue_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.native_issue_tracker_status import NativeIssueTrackerStatus

        d = dict(src_dict)
        enabled = d.pop("enabled")

        expected_revision = d.pop("expected_revision")

        _expected_status = d.pop("expected_status", UNSET)
        expected_status: NativeIssueTrackerStatus | Unset
        if isinstance(_expected_status, Unset):
            expected_status = UNSET
        else:
            expected_status = NativeIssueTrackerStatus.from_dict(_expected_status)

        _expected_issue_state = d.pop("expected_issue_state", UNSET)
        expected_issue_state: UpdateIssueTrackerAutoSyncRequestExpectedIssueState | Unset
        if isinstance(_expected_issue_state, Unset):
            expected_issue_state = UNSET
        else:
            expected_issue_state = check_update_issue_tracker_auto_sync_request_expected_issue_state(
                _expected_issue_state
            )

        update_issue_tracker_auto_sync_request = cls(
            enabled=enabled,
            expected_revision=expected_revision,
            expected_status=expected_status,
            expected_issue_state=expected_issue_state,
        )

        return update_issue_tracker_auto_sync_request
