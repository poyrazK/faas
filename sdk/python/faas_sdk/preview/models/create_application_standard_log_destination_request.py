from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_application_standard_log_destination_request_kind import (
    CreateApplicationStandardLogDestinationRequestKind,
    check_create_application_standard_log_destination_request_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateApplicationStandardLogDestinationRequest")


@_attrs_define
class CreateApplicationStandardLogDestinationRequest:
    """A logging endpoint and optional write-only credential to seal."""

    name: str
    kind: CreateApplicationStandardLogDestinationRequestKind
    target_url: str
    """HTTPS endpoint without userinfo, query string or fragment. Put credentials in auth_header."""
    auth_header: str | Unset = UNSET
    """One Name/value header, sealed server-side and never returned."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind: str = self.kind

        target_url = self.target_url

        auth_header = self.auth_header

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "kind": kind,
                "target_url": target_url,
            }
        )
        if auth_header is not UNSET:
            field_dict["auth_header"] = auth_header

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        kind = check_create_application_standard_log_destination_request_kind(d.pop("kind"))

        target_url = d.pop("target_url")

        auth_header = d.pop("auth_header", UNSET)

        create_application_standard_log_destination_request = cls(
            name=name,
            kind=kind,
            target_url=target_url,
            auth_header=auth_header,
        )

        return create_application_standard_log_destination_request
