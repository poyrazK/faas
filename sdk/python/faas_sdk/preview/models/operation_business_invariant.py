from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_business_invariant_status import (
    OperationBusinessInvariantStatus,
    check_operation_business_invariant_status,
)

T = TypeVar("T", bound="OperationBusinessInvariant")


@_attrs_define
class OperationBusinessInvariant:
    """Application-evaluated business condition. String limits are UTF-8 bytes; instance, version, and description must be
    nonempty without control characters.

    """

    workflow: str
    instance_id: str
    state: str
    code: str
    version: str
    status: OperationBusinessInvariantStatus
    description: str
    operations: list[str]

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        code = self.code

        version = self.version

        status: str = self.status

        description = self.description

        operations = self.operations

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "code": code,
                "version": version,
                "status": status,
                "description": description,
                "operations": operations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        code = d.pop("code")

        version = d.pop("version")

        status = check_operation_business_invariant_status(d.pop("status"))

        description = d.pop("description")

        operations = cast(list[str], d.pop("operations"))

        operation_business_invariant = cls(
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            code=code,
            version=version,
            status=status,
            description=description,
            operations=operations,
        )

        return operation_business_invariant
