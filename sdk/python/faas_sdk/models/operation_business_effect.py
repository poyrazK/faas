from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_effect_status import (
    OperationBusinessEffectStatus,
    check_operation_business_effect_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationBusinessEffect")


@_attrs_define
class OperationBusinessEffect:
    """Application-reported effect. Text bounds are UTF-8 bytes; confirmed status requires a nonempty reference.
    Amount/currency are supplied together in minor units.

    """

    workflow: str
    instance_id: str
    state: str
    operation: str
    code: str
    version: str
    description: str
    status: OperationBusinessEffectStatus
    reference: str | Unset = UNSET
    amount_minor: int | Unset = UNSET
    currency: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        operation = self.operation

        code = self.code

        version = self.version

        description = self.description

        status: str = self.status

        reference = self.reference

        amount_minor = self.amount_minor

        currency = self.currency

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
            }
        )
        if reference is not UNSET:
            field_dict["reference"] = reference
        if amount_minor is not UNSET:
            field_dict["amount_minor"] = amount_minor
        if currency is not UNSET:
            field_dict["currency"] = currency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        operation = d.pop("operation")

        code = d.pop("code")

        version = d.pop("version")

        description = d.pop("description")

        status = check_operation_business_effect_status(d.pop("status"))

        reference = d.pop("reference", UNSET)

        amount_minor = d.pop("amount_minor", UNSET)

        currency = d.pop("currency", UNSET)

        operation_business_effect = cls(
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            operation=operation,
            code=code,
            version=version,
            description=description,
            status=status,
            reference=reference,
            amount_minor=amount_minor,
            currency=currency,
        )

        return operation_business_effect
