from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutIssueTrackerLinkRequest")


@_attrs_define
class PutIssueTrackerLinkRequest:
    """Explicit provider ticket URL and optional title to attach to an issue group."""

    url: str
    """Public GitHub issue or Linear issue HTTPS URL; maximum 512 UTF-8 bytes."""
    title: str | Unset = UNSET
    """Optional display title; maximum 256 UTF-8 bytes without control characters."""

    def to_dict(self) -> dict[str, Any]:
        url = self.url

        title = self.title

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "url": url,
            }
        )
        if title is not UNSET:
            field_dict["title"] = title

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        url = d.pop("url")

        title = d.pop("title", UNSET)

        put_issue_tracker_link_request = cls(
            url=url,
            title=title,
        )

        return put_issue_tracker_link_request
