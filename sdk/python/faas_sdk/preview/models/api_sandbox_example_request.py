from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_example_request_method import (
    APISandboxExampleRequestMethod,
    check_api_sandbox_example_request_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_json_assertion import APISandboxJSONAssertion


T = TypeVar("T", bound="APISandboxExampleRequest")


@_attrs_define
class APISandboxExampleRequest:
    title: str
    method: APISandboxExampleRequestMethod
    path: str
    """Same-host path and optional query."""
    expected_status: int
    body: Any | Unset = UNSET
    """Optional JSON value for a request body. Limit of 2 KiB."""
    assertions: list[APISandboxJSONAssertion] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        method: str = self.method

        path = self.path

        expected_status = self.expected_status

        body = self.body

        assertions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.assertions, Unset):
            assertions = []
            for assertions_item_data in self.assertions:
                assertions_item = assertions_item_data.to_dict()
                assertions.append(assertions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "title": title,
                "method": method,
                "path": path,
                "expected_status": expected_status,
            }
        )
        if body is not UNSET:
            field_dict["body"] = body
        if assertions is not UNSET:
            field_dict["assertions"] = assertions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_json_assertion import APISandboxJSONAssertion

        d = dict(src_dict)
        title = d.pop("title")

        method = check_api_sandbox_example_request_method(d.pop("method"))

        path = d.pop("path")

        expected_status = d.pop("expected_status")

        body = d.pop("body", UNSET)

        _assertions = d.pop("assertions", UNSET)
        assertions: list[APISandboxJSONAssertion] | Unset = UNSET
        if _assertions is not UNSET:
            assertions = []
            for assertions_item_data in _assertions:
                assertions_item = APISandboxJSONAssertion.from_dict(assertions_item_data)

                assertions.append(assertions_item)

        api_sandbox_example_request = cls(
            title=title,
            method=method,
            path=path,
            expected_status=expected_status,
            body=body,
            assertions=assertions,
        )

        api_sandbox_example_request.additional_properties = d
        return api_sandbox_example_request

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
