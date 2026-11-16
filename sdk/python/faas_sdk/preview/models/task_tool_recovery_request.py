from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification


T = TypeVar("T", bound="TaskToolRecoveryRequest")


@_attrs_define
class TaskToolRecoveryRequest:
    """Owner-only handover or qualification verdict on the existing decision route. The authenticated native handler
    supplies the owner identity; agent inputs cannot supply it.

    """

    handover_id: str
    operation_id: str
    revision: int
    qualification: TaskToolRecoveryQualification | Unset = UNSET
    """Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed
    deadline is observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving
    never renews it."""
    qualification_sha256: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        handover_id = self.handover_id

        operation_id = self.operation_id

        revision = self.revision

        qualification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.qualification, Unset):
            qualification = self.qualification.to_dict()

        qualification_sha256 = self.qualification_sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "handover_id": handover_id,
                "operation_id": operation_id,
                "revision": revision,
            }
        )
        if qualification is not UNSET:
            field_dict["qualification"] = qualification
        if qualification_sha256 is not UNSET:
            field_dict["qualification_sha256"] = qualification_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification

        d = dict(src_dict)
        handover_id = d.pop("handover_id")

        operation_id = d.pop("operation_id")

        revision = d.pop("revision")

        _qualification = d.pop("qualification", UNSET)
        qualification: TaskToolRecoveryQualification | Unset
        if isinstance(_qualification, Unset):
            qualification = UNSET
        else:
            qualification = TaskToolRecoveryQualification.from_dict(_qualification)

        qualification_sha256 = d.pop("qualification_sha256", UNSET)

        task_tool_recovery_request = cls(
            handover_id=handover_id,
            operation_id=operation_id,
            revision=revision,
            qualification=qualification,
            qualification_sha256=qualification_sha256,
        )

        return task_tool_recovery_request
