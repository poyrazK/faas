from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplyAppOpenAPIPolicyRequest")


@_attrs_define
class ApplyAppOpenAPIPolicyRequest:
    """Controls the explicit OpenAPI policy plan/apply workflow. Omit the
    body (or set confirm=false) to request a read-only plan. A confirmed
    apply must include the preview_sha256 returned by that plan.

    """

    confirm: bool | Unset = False
    """Authorize creation of the generated validation rules."""
    preview_sha256: str | Unset = UNSET
    """Approval token returned by the current plan."""
    match_host: str | Unset = UNSET
    """Hostname for generated rules; defaults to the app hostname."""

    def to_dict(self) -> dict[str, Any]:
        confirm = self.confirm

        preview_sha256 = self.preview_sha256

        match_host = self.match_host

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if confirm is not UNSET:
            field_dict["confirm"] = confirm
        if preview_sha256 is not UNSET:
            field_dict["preview_sha256"] = preview_sha256
        if match_host is not UNSET:
            field_dict["match_host"] = match_host

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        confirm = d.pop("confirm", UNSET)

        preview_sha256 = d.pop("preview_sha256", UNSET)

        match_host = d.pop("match_host", UNSET)

        apply_app_open_api_policy_request = cls(
            confirm=confirm,
            preview_sha256=preview_sha256,
            match_host=match_host,
        )

        return apply_app_open_api_policy_request
