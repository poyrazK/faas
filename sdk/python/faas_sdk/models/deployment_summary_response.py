from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_change import DeploymentChange
    from ..models.deployment_response import DeploymentResponse


T = TypeVar("T", bound="DeploymentSummaryResponse")


@_attrs_define
class DeploymentSummaryResponse:
    """App-scoped release cockpit: selected deployment, immediate predecessor, non-secret field-level diff, and eligible
    rollback target.

    """

    deployment: DeploymentResponse
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last
    deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed
    because the ref shape is non-secret by design."""
    changes: list[DeploymentChange]
    previous: DeploymentResponse | None | Unset = UNSET
    """The immediately older deployment by created_at, or null for an initial release."""
    rollback_target_id: None | Unset | UUID = UNSET
    """The latest superseded deployment eligible for POST /v1/apps/{slug}/rollback; omitted when none exists."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.deployment_response import DeploymentResponse

        deployment = self.deployment.to_dict()

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        previous: dict[str, Any] | None | Unset
        if isinstance(self.previous, Unset):
            previous = UNSET
        elif isinstance(self.previous, DeploymentResponse):
            previous = self.previous.to_dict()
        else:
            previous = self.previous

        rollback_target_id: None | str | Unset
        if isinstance(self.rollback_target_id, Unset):
            rollback_target_id = UNSET
        elif isinstance(self.rollback_target_id, UUID):
            rollback_target_id = str(self.rollback_target_id)
        else:
            rollback_target_id = self.rollback_target_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment": deployment,
                "changes": changes,
            }
        )
        if previous is not UNSET:
            field_dict["previous"] = previous
        if rollback_target_id is not UNSET:
            field_dict["rollback_target_id"] = rollback_target_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_change import DeploymentChange
        from ..models.deployment_response import DeploymentResponse

        d = dict(src_dict)
        deployment = DeploymentResponse.from_dict(d.pop("deployment"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = DeploymentChange.from_dict(changes_item_data)

            changes.append(changes_item)

        def _parse_previous(data: object) -> DeploymentResponse | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                previous_type_0 = DeploymentResponse.from_dict(data)

                return previous_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DeploymentResponse | None | Unset, data)

        previous = _parse_previous(d.pop("previous", UNSET))

        def _parse_rollback_target_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                rollback_target_id_type_0 = UUID(data)

                return rollback_target_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        rollback_target_id = _parse_rollback_target_id(d.pop("rollback_target_id", UNSET))

        deployment_summary_response = cls(
            deployment=deployment,
            changes=changes,
            previous=previous,
            rollback_target_id=rollback_target_id,
        )

        deployment_summary_response.additional_properties = d
        return deployment_summary_response

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
