from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_compensation_status import (
    OperationBusinessCompensationStatus,
    check_operation_business_compensation_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_effect_reference import OperationBusinessEffectReference


T = TypeVar("T", bound="OperationBusinessCompensation")


@_attrs_define
class OperationBusinessCompensation:
    """Application-reported compensation workflow observation. Confirmed status requires a nonempty reference. Text bounds
    are UTF-8 bytes without control characters. Source must be a retained confirmed effect in the same account, app,
    customer, and environment.

    """

    workflow: str
    instance_id: str
    state: str
    operation: str
    code: str
    version: str
    description: str
    status: OperationBusinessCompensationStatus
    source_effect: OperationBusinessEffectReference
    reference: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        operation = self.operation

        code = self.code

        version = self.version

        description = self.description

        status: str = self.status

        source_effect = self.source_effect.to_dict()

        reference = self.reference

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "operation": operation,
                "code": code,
                "version": version,
                "description": description,
                "status": status,
                "source_effect": source_effect,
            }
        )
        if reference is not UNSET:
            field_dict["reference"] = reference

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_effect_reference import OperationBusinessEffectReference

        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        operation = d.pop("operation")

        code = d.pop("code")

        version = d.pop("version")

        description = d.pop("description")

        status = check_operation_business_compensation_status(d.pop("status"))

        source_effect = OperationBusinessEffectReference.from_dict(d.pop("source_effect"))

        reference = d.pop("reference", UNSET)

        operation_business_compensation = cls(
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            operation=operation,
            code=code,
            version=version,
            description=description,
            status=status,
            source_effect=source_effect,
            reference=reference,
        )

        return operation_business_compensation
