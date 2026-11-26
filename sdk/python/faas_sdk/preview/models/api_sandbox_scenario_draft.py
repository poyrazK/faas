from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_scenario_draft_method import (
    APISandboxScenarioDraftMethod,
    check_api_sandbox_scenario_draft_method,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxScenarioDraft")


@_attrs_define
class APISandboxScenarioDraft:
    """Safe scenario starting point derived from a retained request."""

    deployment_id: UUID
    source_request_id: UUID
    expected_revision: str
    """Current scenario revision or an empty string when the release has no saved scenario."""
    method: APISandboxScenarioDraftMethod
    route_template: str | Unset = UNSET
    """The matching declared route or stored OpenAPI path, when available. Replace placeholders with synthetic
    values before saving."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        source_request_id = str(self.source_request_id)

        expected_revision = self.expected_revision

        method: str = self.method

        route_template = self.route_template

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "source_request_id": source_request_id,
                "expected_revision": expected_revision,
                "method": method,
            }
        )
        if route_template is not UNSET:
            field_dict["route_template"] = route_template

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        source_request_id = UUID(d.pop("source_request_id"))

        expected_revision = d.pop("expected_revision")

        method = check_api_sandbox_scenario_draft_method(d.pop("method"))

        route_template = d.pop("route_template", UNSET)

        api_sandbox_scenario_draft = cls(
            deployment_id=deployment_id,
            source_request_id=source_request_id,
            expected_revision=expected_revision,
            method=method,
            route_template=route_template,
        )

        api_sandbox_scenario_draft.additional_properties = d
        return api_sandbox_scenario_draft

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
