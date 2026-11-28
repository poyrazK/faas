from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.env_batch_request_values import EnvBatchRequestValues


T = TypeVar("T", bound="EnvBatchRequest")


@_attrs_define
class EnvBatchRequest:
    """At least one set or delete is required. Keys must be distinct across both collections; plan limits bound collection
    counts and value sizes.

    """

    request_id: UUID
    expected_revision: int
    expected_app_id: UUID | Unset = UNSET
    """Optional immutable app identity; required when expected_environment_id is supplied."""
    expected_environment_id: UUID | Unset = UNSET
    """Immutable named-environment identity for an identity-pinned batch; omit for the default scope."""
    receipt_proof: str | Unset = UNSET
    """Optional private random proof required to read this batch receipt; never returned in a receipt. Requires
    expected_app_id."""
    values: EnvBatchRequestValues | Unset = UNSET
    """Non-secret variable values to set, omitted from receipts."""
    delete: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        expected_revision = self.expected_revision

        expected_app_id: str | Unset = UNSET
        if not isinstance(self.expected_app_id, Unset):
            expected_app_id = str(self.expected_app_id)

        expected_environment_id: str | Unset = UNSET
        if not isinstance(self.expected_environment_id, Unset):
            expected_environment_id = str(self.expected_environment_id)

        receipt_proof = self.receipt_proof

        values: dict[str, Any] | Unset = UNSET
        if not isinstance(self.values, Unset):
            values = self.values.to_dict()

        delete: list[str] | Unset = UNSET
        if not isinstance(self.delete, Unset):
            delete = self.delete

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_revision": expected_revision,
            }
        )
        if expected_app_id is not UNSET:
            field_dict["expected_app_id"] = expected_app_id
        if expected_environment_id is not UNSET:
            field_dict["expected_environment_id"] = expected_environment_id
        if receipt_proof is not UNSET:
            field_dict["receipt_proof"] = receipt_proof
        if values is not UNSET:
            field_dict["values"] = values
        if delete is not UNSET:
            field_dict["delete"] = delete

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.env_batch_request_values import EnvBatchRequestValues

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        expected_revision = d.pop("expected_revision")

        _expected_app_id = d.pop("expected_app_id", UNSET)
        expected_app_id: UUID | Unset
        if isinstance(_expected_app_id, Unset):
            expected_app_id = UNSET
        else:
            expected_app_id = UUID(_expected_app_id)

        _expected_environment_id = d.pop("expected_environment_id", UNSET)
        expected_environment_id: UUID | Unset
        if isinstance(_expected_environment_id, Unset):
            expected_environment_id = UNSET
        else:
            expected_environment_id = UUID(_expected_environment_id)

        receipt_proof = d.pop("receipt_proof", UNSET)

        _values = d.pop("values", UNSET)
        values: EnvBatchRequestValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = EnvBatchRequestValues.from_dict(_values)

        delete = cast(list[str], d.pop("delete", UNSET))

        env_batch_request = cls(
            request_id=request_id,
            expected_revision=expected_revision,
            expected_app_id=expected_app_id,
            expected_environment_id=expected_environment_id,
            receipt_proof=receipt_proof,
            values=values,
            delete=delete,
        )

        return env_batch_request
