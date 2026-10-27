from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.update_issue_tracker_auto_sync_request_direction import (
    UpdateIssueTrackerAutoSyncRequestDirection,
    check_update_issue_tracker_auto_sync_request_direction,
)
from ..models.update_issue_tracker_auto_sync_request_expected_issue_state import (
    UpdateIssueTrackerAutoSyncRequestExpectedIssueState,
    check_update_issue_tracker_auto_sync_request_expected_issue_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.native_issue_tracker_status import NativeIssueTrackerStatus
    from ..models.native_linear_issue_tracker_status import NativeLinearIssueTrackerStatus


T = TypeVar("T", bound="UpdateIssueTrackerAutoSyncRequest")


@_attrs_define
class UpdateIssueTrackerAutoSyncRequest:
    """Explicit direction delegation against a known policy revision and aligned native/source lifecycle snapshot. Inbound
    closures are held for fixing-deployment review.

    """

    enabled: bool
    expected_revision: int
    direction: UpdateIssueTrackerAutoSyncRequestDirection | Unset = UNSET
    """Defaults to outbound when omitted. Inbound and both require a fresh source intent baseline."""
    expected_status: NativeIssueTrackerStatus | Unset = UNSET
    """Verified native connection and observed status of a linked tracker issue."""
    expected_issue_state: UpdateIssueTrackerAutoSyncRequestExpectedIssueState | Unset = UNSET
    expected_linear_status: NativeLinearIssueTrackerStatus | Unset = UNSET
    """Credential-free Linear lifecycle preview pinned to the current issue group and owner connection."""
    linear_open_state_id: UUID | Unset = UNSET
    """Active same-team open workflow required to enable Linear automatic sync."""
    linear_resolved_state_id: UUID | Unset = UNSET
    """Active same-team completed workflow required to enable Linear automatic sync."""

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        expected_revision = self.expected_revision

        direction: str | Unset = UNSET
        if not isinstance(self.direction, Unset):
            direction = self.direction

        expected_status: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_status, Unset):
            expected_status = self.expected_status.to_dict()

        expected_issue_state: str | Unset = UNSET
        if not isinstance(self.expected_issue_state, Unset):
            expected_issue_state = self.expected_issue_state

        expected_linear_status: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_linear_status, Unset):
            expected_linear_status = self.expected_linear_status.to_dict()

        linear_open_state_id: str | Unset = UNSET
        if not isinstance(self.linear_open_state_id, Unset):
            linear_open_state_id = str(self.linear_open_state_id)

        linear_resolved_state_id: str | Unset = UNSET
        if not isinstance(self.linear_resolved_state_id, Unset):
            linear_resolved_state_id = str(self.linear_resolved_state_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "expected_revision": expected_revision,
            }
        )
        if direction is not UNSET:
            field_dict["direction"] = direction
        if expected_status is not UNSET:
            field_dict["expected_status"] = expected_status
        if expected_issue_state is not UNSET:
            field_dict["expected_issue_state"] = expected_issue_state
        if expected_linear_status is not UNSET:
            field_dict["expected_linear_status"] = expected_linear_status
        if linear_open_state_id is not UNSET:
            field_dict["linear_open_state_id"] = linear_open_state_id
        if linear_resolved_state_id is not UNSET:
            field_dict["linear_resolved_state_id"] = linear_resolved_state_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.native_issue_tracker_status import NativeIssueTrackerStatus
        from ..models.native_linear_issue_tracker_status import NativeLinearIssueTrackerStatus

        d = dict(src_dict)
        enabled = d.pop("enabled")

        expected_revision = d.pop("expected_revision")

        _direction = d.pop("direction", UNSET)
        direction: UpdateIssueTrackerAutoSyncRequestDirection | Unset
        if isinstance(_direction, Unset):
            direction = UNSET
        else:
            direction = check_update_issue_tracker_auto_sync_request_direction(_direction)

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

        _expected_linear_status = d.pop("expected_linear_status", UNSET)
        expected_linear_status: NativeLinearIssueTrackerStatus | Unset
        if isinstance(_expected_linear_status, Unset):
            expected_linear_status = UNSET
        else:
            expected_linear_status = NativeLinearIssueTrackerStatus.from_dict(_expected_linear_status)

        _linear_open_state_id = d.pop("linear_open_state_id", UNSET)
        linear_open_state_id: UUID | Unset
        if isinstance(_linear_open_state_id, Unset):
            linear_open_state_id = UNSET
        else:
            linear_open_state_id = UUID(_linear_open_state_id)

        _linear_resolved_state_id = d.pop("linear_resolved_state_id", UNSET)
        linear_resolved_state_id: UUID | Unset
        if isinstance(_linear_resolved_state_id, Unset):
            linear_resolved_state_id = UNSET
        else:
            linear_resolved_state_id = UUID(_linear_resolved_state_id)

        update_issue_tracker_auto_sync_request = cls(
            enabled=enabled,
            expected_revision=expected_revision,
            direction=direction,
            expected_status=expected_status,
            expected_issue_state=expected_issue_state,
            expected_linear_status=expected_linear_status,
            linear_open_state_id=linear_open_state_id,
            linear_resolved_state_id=linear_resolved_state_id,
        )

        return update_issue_tracker_auto_sync_request
