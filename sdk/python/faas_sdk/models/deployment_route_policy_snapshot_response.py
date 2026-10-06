from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.edge_rule_response import EdgeRuleResponse


T = TypeVar("T", bound="DeploymentRoutePolicySnapshotResponse")


@_attrs_define
class DeploymentRoutePolicySnapshotResponse:
    """Immutable gateway edge-rule state recorded on the deployment's first live transition."""

    deployment_id: UUID
    app_id: UUID
    scope: str
    sha256: str
    schema_version: int
    captured_at: datetime.datetime
    rules: list[EdgeRuleResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        app_id = str(self.app_id)

        scope = self.scope

        sha256 = self.sha256

        schema_version = self.schema_version

        captured_at = self.captured_at.isoformat()

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "app_id": app_id,
                "scope": scope,
                "sha256": sha256,
                "schema_version": schema_version,
                "captured_at": captured_at,
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_response import EdgeRuleResponse

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        sha256 = d.pop("sha256")

        schema_version = d.pop("schema_version")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = EdgeRuleResponse.from_dict(rules_item_data)

            rules.append(rules_item)

        deployment_route_policy_snapshot_response = cls(
            deployment_id=deployment_id,
            app_id=app_id,
            scope=scope,
            sha256=sha256,
            schema_version=schema_version,
            captured_at=captured_at,
            rules=rules,
        )

        deployment_route_policy_snapshot_response.additional_properties = d
        return deployment_route_policy_snapshot_response

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
