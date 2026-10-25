from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_result_artifact import OperationResultArtifact


T = TypeVar("T", bound="OperationArtifactUploadResponse")


@_attrs_define
class OperationArtifactUploadResponse:
    """Availability and metadata of one verified private HTTP invocation file receipt."""

    available: bool
    """True only for a verified private copy bound to this invocation attempt. Does not confirm business success."""
    artifact: OperationResultArtifact | Unset = UNSET
    """Verified private result reference, from a managed object or direct Job upload."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        artifact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.artifact, Unset):
            artifact = self.artifact.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
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

        operation_artifact_upload_response = cls(
            available=available,
            artifact=artifact,
        )

        operation_artifact_upload_response.additional_properties = d
        return operation_artifact_upload_response

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
