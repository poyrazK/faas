from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_evolution_request_action import (
    DataPipelineEvolutionRequestAction,
    check_data_pipeline_evolution_request_action,
)
from ..models.data_pipeline_evolution_request_kind import (
    DataPipelineEvolutionRequestKind,
    check_data_pipeline_evolution_request_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_auth_organizations import DataPipelineAuthOrganizations
    from ..models.data_pipeline_evolution_request_columns import DataPipelineEvolutionRequestColumns
    from ..models.data_pipeline_relationship import DataPipelineRelationship


T = TypeVar("T", bound="DataPipelineEvolutionRequest")


@_attrs_define
class DataPipelineEvolutionRequest:
    """Revision-fenced owner evolution action. Preview supplies one kind and its complete selection or mapping. Approve and
    discard supply only a saved change_id; approval never accepts replacement intent.

    """

    expected_revision: int
    action: DataPipelineEvolutionRequestAction
    change_id: str | Unset = UNSET
    kind: DataPipelineEvolutionRequestKind | Unset = UNSET
    columns: DataPipelineEvolutionRequestColumns | Unset = UNSET
    auth_organizations: DataPipelineAuthOrganizations | Unset = UNSET
    """Owner-approved Auth organization mapping. Internal binding identifiers are resolved by the control plane and
    rechecked on every snapshot."""
    relationships: list[DataPipelineRelationship] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        action: str = self.action

        change_id = self.change_id

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        columns: dict[str, Any] | Unset = UNSET
        if not isinstance(self.columns, Unset):
            columns = self.columns.to_dict()

        auth_organizations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.auth_organizations, Unset):
            auth_organizations = self.auth_organizations.to_dict()

        relationships: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.relationships, Unset):
            relationships = []
            for relationships_item_data in self.relationships:
                relationships_item = relationships_item_data.to_dict()
                relationships.append(relationships_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "action": action,
            }
        )
        if change_id is not UNSET:
            field_dict["change_id"] = change_id
        if kind is not UNSET:
            field_dict["kind"] = kind
        if columns is not UNSET:
            field_dict["columns"] = columns
        if auth_organizations is not UNSET:
            field_dict["auth_organizations"] = auth_organizations
        if relationships is not UNSET:
            field_dict["relationships"] = relationships

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_auth_organizations import DataPipelineAuthOrganizations
        from ..models.data_pipeline_evolution_request_columns import DataPipelineEvolutionRequestColumns
        from ..models.data_pipeline_relationship import DataPipelineRelationship

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        action = check_data_pipeline_evolution_request_action(d.pop("action"))

        change_id = d.pop("change_id", UNSET)

        _kind = d.pop("kind", UNSET)
        kind: DataPipelineEvolutionRequestKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_data_pipeline_evolution_request_kind(_kind)

        _columns = d.pop("columns", UNSET)
        columns: DataPipelineEvolutionRequestColumns | Unset
        if isinstance(_columns, Unset):
            columns = UNSET
        else:
            columns = DataPipelineEvolutionRequestColumns.from_dict(_columns)

        _auth_organizations = d.pop("auth_organizations", UNSET)
        auth_organizations: DataPipelineAuthOrganizations | Unset
        if isinstance(_auth_organizations, Unset):
            auth_organizations = UNSET
        else:
            auth_organizations = DataPipelineAuthOrganizations.from_dict(_auth_organizations)

        _relationships = d.pop("relationships", UNSET)
        relationships: list[DataPipelineRelationship] | Unset = UNSET
        if _relationships is not UNSET:
            relationships = []
            for relationships_item_data in _relationships:
                relationships_item = DataPipelineRelationship.from_dict(relationships_item_data)

                relationships.append(relationships_item)

        data_pipeline_evolution_request = cls(
            expected_revision=expected_revision,
            action=action,
            change_id=change_id,
            kind=kind,
            columns=columns,
            auth_organizations=auth_organizations,
            relationships=relationships,
        )

        return data_pipeline_evolution_request
