from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="HostedTaskSourceReviewSourceSha256")


@_attrs_define
class HostedTaskSourceReviewSourceSha256:
    web: str
    worker: str

    def to_dict(self) -> dict[str, Any]:
        web = self.web

        worker = self.worker

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "web": web,
                "worker": worker,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        web = d.pop("web")

        worker = d.pop("worker")

        hosted_task_source_review_source_sha_256 = cls(
            web=web,
            worker=worker,
        )

        return hosted_task_source_review_source_sha_256
