from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_result_artifact import OperationResultArtifact


T = TypeVar("T", bound="OperationWorkflowArtifactResponse")


@_attrs_define
class OperationWorkflowArtifactResponse:
    """A private verified workflow copy receipt; publication follows confirmed execution success."""

    available: bool
    """False only after successful authorization with no matching verified copy."""
    artifact: OperationResultArtifact | Unset = UNSET
    """Verified private result reference, from a managed object or direct Job upload."""

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        artifact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.artifact, Unset):
            artifact = self.artifact.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "available": available,
            }
        )
        if artifact is not UNSET:
            field_dict["artifact"] = artifact

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_result_artifact import OperationResultArtifact

        d = dict(src_dict)
        available = d.pop("available")

        _artifact = d.pop("artifact", UNSET)
        artifact: OperationResultArtifact | Unset
        if isinstance(_artifact, Unset):
            artifact = UNSET
        else:
            artifact = OperationResultArtifact.from_dict(_artifact)

        operation_workflow_artifact_response = cls(
            available=available,
            artifact=artifact,
        )

        return operation_workflow_artifact_response
