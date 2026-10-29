from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowActionObligation")


@_attrs_define
class OperationWorkflowActionObligation:
    """Stable source-and-kind identity derived from the persisted obligation epoch, including dependency-review revision
    where available. Ownership changes and deadline edits preserve the identity except a newly scheduled response check-
    in, which defines a new check-in obligation. Older saved actions may omit identity.

    """

    id: str
    started_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        started_at = self.started_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "started_at": started_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        operation_workflow_action_obligation = cls(
            id=id,
            started_at=started_at,
        )

        return operation_workflow_action_obligation
