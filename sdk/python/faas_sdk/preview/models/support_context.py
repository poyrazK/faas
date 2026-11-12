from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SupportContext")


@_attrs_define
class SupportContext:
    """Optional identifiers supplied by the customer; no automatic log, secret or source-code collection."""

    app_id: UUID | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    request_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        request_id = self.request_id

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if request_id is not UNSET:
            field_dict["request_id"] = request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        request_id = d.pop("request_id", UNSET)

        support_context = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            request_id=request_id,
        )

        return support_context
