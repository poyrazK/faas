from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="InvokeWork")


@_attrs_define
class InvokeWork:
    """Named policy and typed application key for one async invocation."""

    policy: str
    key: Any
    """A bounded JSON string, number, or boolean. Equal typed values share one work lane."""
    fairness_key: Any | Unset = UNSET
    """Optional scalar shared by multiple work keys, such as a tenant ID. Defaults to key when the policy has a
    fairness cap."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy

        key = self.key

        fairness_key = self.fairness_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "key": key,
            }
        )
        if fairness_key is not UNSET:
            field_dict["fairness_key"] = fairness_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        policy = d.pop("policy")

        key = d.pop("key")

        fairness_key = d.pop("fairness_key", UNSET)

        invoke_work = cls(
            policy=policy,
            key=key,
            fairness_key=fairness_key,
        )

        return invoke_work
