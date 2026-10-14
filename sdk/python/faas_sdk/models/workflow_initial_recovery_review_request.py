from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowInitialRecoveryReviewRequest")


@_attrs_define
class WorkflowInitialRecoveryReviewRequest:
    """Parameters for preparing a durable initial recovery review."""

    expected_policy_version: int

    def to_dict(self) -> dict[str, Any]:
        expected_policy_version = self.expected_policy_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_policy_version": expected_policy_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_policy_version = d.pop("expected_policy_version")

        workflow_initial_recovery_review_request = cls(
            expected_policy_version=expected_policy_version,
        )

        return workflow_initial_recovery_review_request
