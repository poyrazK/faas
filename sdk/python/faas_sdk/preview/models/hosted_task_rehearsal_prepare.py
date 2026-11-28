from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="HostedTaskRehearsalPrepare")


@_attrs_define
class HostedTaskRehearsalPrepare:
    """Exact digests of the approved source and passing native qualification."""

    intent_sha256: str
    review_sha256: str
    binding_sha256: str

    def to_dict(self) -> dict[str, Any]:
        intent_sha256 = self.intent_sha256

        review_sha256 = self.review_sha256

        binding_sha256 = self.binding_sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_sha256": intent_sha256,
                "review_sha256": review_sha256,
                "binding_sha256": binding_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        intent_sha256 = d.pop("intent_sha256")

        review_sha256 = d.pop("review_sha256")

        binding_sha256 = d.pop("binding_sha256")

        hosted_task_rehearsal_prepare = cls(
            intent_sha256=intent_sha256,
            review_sha256=review_sha256,
            binding_sha256=binding_sha256,
        )

        return hosted_task_rehearsal_prepare
