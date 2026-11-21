from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_environment_response_runtime import (
    ExecutionEnvironmentResponseRuntime,
    check_execution_environment_response_runtime,
)
from ..models.execution_environment_response_status import (
    ExecutionEnvironmentResponseStatus,
    check_execution_environment_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionEnvironmentResponse")


@_attrs_define
class ExecutionEnvironmentResponse:
    """Dependency environment identity, build status and available image metadata."""

    id: UUID
    runtime: ExecutionEnvironmentResponseRuntime
    status: ExecutionEnvironmentResponseStatus
    recipe_digest: str
    created_at: datetime.datetime
    image_digest: str | Unset = UNSET
    artifact_bytes: int | Unset = UNSET
    failure_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        runtime: str = self.runtime

        status: str = self.status

        recipe_digest = self.recipe_digest

        created_at = self.created_at.isoformat()

        image_digest = self.image_digest

        artifact_bytes = self.artifact_bytes

        failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "runtime": runtime,
                "status": status,
                "recipe_digest": recipe_digest,
                "created_at": created_at,
            }
        )
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if artifact_bytes is not UNSET:
            field_dict["artifact_bytes"] = artifact_bytes
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        runtime = check_execution_environment_response_runtime(d.pop("runtime"))

        status = check_execution_environment_response_status(d.pop("status"))

        recipe_digest = d.pop("recipe_digest")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        image_digest = d.pop("image_digest", UNSET)

        artifact_bytes = d.pop("artifact_bytes", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        execution_environment_response = cls(
            id=id,
            runtime=runtime,
            status=status,
            recipe_digest=recipe_digest,
            created_at=created_at,
            image_digest=image_digest,
            artifact_bytes=artifact_bytes,
            failure_code=failure_code,
        )

        execution_environment_response.additional_properties = d
        return execution_environment_response

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
