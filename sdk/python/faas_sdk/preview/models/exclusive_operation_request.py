from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.exclusive_operation_request_invocation import ExclusiveOperationRequestInvocation


T = TypeVar("T", bound="ExclusiveOperationRequest")


@_attrs_define
class ExclusiveOperationRequest:
    """Work request admitted under a named exclusive-operation policy."""

    policy: str
    key: bool | float | str
    """JSON scalar used only as a business coordination key; account and tenant scope come from authenticated
    platform context."""
    invocation: ExclusiveOperationRequestInvocation
    equivalence_key: str | Unset = UNSET
    """Optional identity for joining accepted app invocations only when their invocation intents are equivalent."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy

        key: bool | float | str
        key = self.key

        invocation = self.invocation.to_dict()

        equivalence_key = self.equivalence_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "key": key,
                "invocation": invocation,
            }
        )
        if equivalence_key is not UNSET:
            field_dict["equivalence_key"] = equivalence_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.exclusive_operation_request_invocation import ExclusiveOperationRequestInvocation

        d = dict(src_dict)
        policy = d.pop("policy")

        def _parse_key(data: object) -> bool | float | str:
            return cast(bool | float | str, data)

        key = _parse_key(d.pop("key"))

        invocation = ExclusiveOperationRequestInvocation.from_dict(d.pop("invocation"))

        equivalence_key = d.pop("equivalence_key", UNSET)

        exclusive_operation_request = cls(
            policy=policy,
            key=key,
            invocation=invocation,
            equivalence_key=equivalence_key,
        )

        return exclusive_operation_request
