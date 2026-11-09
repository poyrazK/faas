from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.stored_workflow_publish_policy_mode import (
    StoredWorkflowPublishPolicyMode,
    check_stored_workflow_publish_policy_mode,
)

T = TypeVar("T", bound="StoredWorkflowPublishPolicy")


@_attrs_define
class StoredWorkflowPublishPolicy:
    """App policy for dashboard publication. YAML deployment is a separate surface. Changes require admin scope and
    invalidate previously issued receipts.

    """

    mode: StoredWorkflowPublishPolicyMode
    version: int

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_stored_workflow_publish_policy_mode(d.pop("mode"))

        version = d.pop("version")

        stored_workflow_publish_policy = cls(
            mode=mode,
            version=version,
        )

        return stored_workflow_publish_policy
