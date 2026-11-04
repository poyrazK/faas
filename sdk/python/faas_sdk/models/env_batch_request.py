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
    values: EnvBatchRequestValues | Unset = UNSET
    """Non-secret variable values to set, omitted from receipts."""
    delete: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        expected_revision = self.expected_revision

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
            values=values,
            delete=delete,
        )

        return env_batch_request
