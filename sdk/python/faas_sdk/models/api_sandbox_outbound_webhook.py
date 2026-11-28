from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_json_assertion import APISandboxJSONAssertion


T = TypeVar("T", bound="APISandboxOutboundWebhook")


@_attrs_define
class APISandboxOutboundWebhook:
    """Check a managed async job completion callback to a sandbox-owned receiver."""

    retry_once: bool | Unset = UNSET
    """ Return HTTP 503 on the first verified delivery and HTTP 204 on the retry. """
    assertions: list[APISandboxJSONAssertion] | Unset = UNSET
    """ Assertions on the job result carried by the callback. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        retry_once = self.retry_once

        assertions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.assertions, Unset):
            assertions = []
            for assertions_item_data in self.assertions:
                assertions_item = assertions_item_data.to_dict()
                assertions.append(assertions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if retry_once is not UNSET:
            field_dict["retry_once"] = retry_once
        if assertions is not UNSET:
            field_dict["assertions"] = assertions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_json_assertion import APISandboxJSONAssertion

        d = dict(src_dict)
        retry_once = d.pop("retry_once", UNSET)

        _assertions = d.pop("assertions", UNSET)
        assertions: list[APISandboxJSONAssertion] | Unset = UNSET
        if _assertions is not UNSET:
            assertions = []
            for assertions_item_data in _assertions:
                assertions_item = APISandboxJSONAssertion.from_dict(assertions_item_data)

                assertions.append(assertions_item)

        api_sandbox_outbound_webhook = cls(
            retry_once=retry_once,
            assertions=assertions,
        )

        api_sandbox_outbound_webhook.additional_properties = d
        return api_sandbox_outbound_webhook

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
