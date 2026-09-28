from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="InvokeWork")


@_attrs_define
class InvokeWork:
    """Named policy and typed application key for one async invocation."""

    policy: str
    key: Any
    """A bounded JSON string, number, or boolean. Equal typed values share one work lane."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy

        key = self.key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "key": key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        policy = d.pop("policy")

        key = d.pop("key")

        invoke_work = cls(
            policy=policy,
            key=key,
        )

        return invoke_work
