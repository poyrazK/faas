from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_native_linear_issue_tracker_status_request_desired_state import (
    UpdateNativeLinearIssueTrackerStatusRequestDesiredState,
    check_update_native_linear_issue_tracker_status_request_desired_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_tracker_provider_status import IssueTrackerProviderStatus


T = TypeVar("T", bound="UpdateNativeLinearIssueTrackerStatusRequest")


@_attrs_define
class UpdateNativeLinearIssueTrackerStatusRequest:
    """Explicit push against an observed provider snapshot and connection/group scope."""

    desired_state: UpdateNativeLinearIssueTrackerStatusRequestDesiredState
    """Lifecycle matching the current Gregale issue state."""
    expected: IssueTrackerProviderStatus
    """Observed lifecycle and identity of an external tracker issue."""
    expected_connection_revision: int
    """Connection revision from the native preview."""
    expected_group_fingerprint: str
    """Group fingerprint from the native preview."""
    linear_state_id: UUID | Unset = UNSET
    """Explicit active same-team workflow state UUID required when the lifecycle changes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        desired_state: str = self.desired_state

        expected = self.expected.to_dict()

        expected_connection_revision = self.expected_connection_revision

        expected_group_fingerprint = self.expected_group_fingerprint

        linear_state_id: str | Unset = UNSET
        if not isinstance(self.linear_state_id, Unset):
            linear_state_id = str(self.linear_state_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "desired_state": desired_state,
                "expected": expected,
                "expected_connection_revision": expected_connection_revision,
                "expected_group_fingerprint": expected_group_fingerprint,
            }
        )
        if linear_state_id is not UNSET:
            field_dict["linear_state_id"] = linear_state_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_tracker_provider_status import IssueTrackerProviderStatus

        d = dict(src_dict)
        desired_state = check_update_native_linear_issue_tracker_status_request_desired_state(d.pop("desired_state"))

        expected = IssueTrackerProviderStatus.from_dict(d.pop("expected"))

        expected_connection_revision = d.pop("expected_connection_revision")

        expected_group_fingerprint = d.pop("expected_group_fingerprint")

        _linear_state_id = d.pop("linear_state_id", UNSET)
        linear_state_id: UUID | Unset
        if isinstance(_linear_state_id, Unset):
            linear_state_id = UNSET
        else:
            linear_state_id = UUID(_linear_state_id)

        update_native_linear_issue_tracker_status_request = cls(
            desired_state=desired_state,
            expected=expected,
            expected_connection_revision=expected_connection_revision,
            expected_group_fingerprint=expected_group_fingerprint,
            linear_state_id=linear_state_id,
        )

        update_native_linear_issue_tracker_status_request.additional_properties = d
        return update_native_linear_issue_tracker_status_request

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
