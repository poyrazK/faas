from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_stored_workflow_publish_policy_request_mode import (
    SetStoredWorkflowPublishPolicyRequestMode,
    check_set_stored_workflow_publish_policy_request_mode,
)

T = TypeVar("T", bound="SetStoredWorkflowPublishPolicyRequest")


@_attrs_define
class SetStoredWorkflowPublishPolicyRequest:
    """Versioned app policy change; requires an admin credential."""

    mode: SetStoredWorkflowPublishPolicyRequestMode
    expected_version: int

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        expected_version = self.expected_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
                "expected_version": expected_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_set_stored_workflow_publish_policy_request_mode(d.pop("mode"))

        expected_version = d.pop("expected_version")

        set_stored_workflow_publish_policy_request = cls(
            mode=mode,
            expected_version=expected_version,
        )

        return set_stored_workflow_publish_policy_request
