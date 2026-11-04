from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_verification_watch_item import (
        OperationWorkflowReadinessVerificationWatchItem,
    )


T = TypeVar("T", bound="OperationWorkflowReadinessVerificationWatch")


@_attrs_define
class OperationWorkflowReadinessVerificationWatch:
    """Opt-in watch of captured resolution identities. Verified proofs remain observed for later loss or explicit blocker
    reopening; partial coverage and source inbox retention remain explicit.

    """

    id: UUID
    """OperationWorkflowReadinessVerificationWatch.id. Server-managed advisory verification observation for the
    retained source completion."""
    revision: int
    """OperationWorkflowReadinessVerificationWatch.revision. Server-managed advisory verification observation for
    the retained source completion."""
    started_at: datetime.datetime
    """OperationWorkflowReadinessVerificationWatch.started_at. Server-managed advisory verification observation for
    the retained source completion."""
    evaluated_at: datetime.datetime
    """OperationWorkflowReadinessVerificationWatch.evaluated_at. Server-managed advisory verification observation
    for the retained source completion."""
    state_revision: int
    """OperationWorkflowReadinessVerificationWatch.state_revision. Server-managed advisory verification observation
    for the retained source completion."""
    contract_version: int
    """OperationWorkflowReadinessVerificationWatch.contract_version. Server-managed advisory verification
    observation for the retained source completion."""
    pending_count_at_start: int
    """OperationWorkflowReadinessVerificationWatch.pending_count_at_start. Server-managed advisory verification
    observation for the retained source completion."""
    coverage_incomplete: bool
    """OperationWorkflowReadinessVerificationWatch.coverage_incomplete. Server-managed advisory verification
    observation for the retained source completion."""
    items: list[OperationWorkflowReadinessVerificationWatchItem]
    """OperationWorkflowReadinessVerificationWatch.items. Server-managed advisory verification observation for the
    retained source completion."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = self.revision

        started_at = self.started_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        state_revision = self.state_revision

        contract_version = self.contract_version

        pending_count_at_start = self.pending_count_at_start

        coverage_incomplete = self.coverage_incomplete

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "started_at": started_at,
                "evaluated_at": evaluated_at,
                "state_revision": state_revision,
                "contract_version": contract_version,
                "pending_count_at_start": pending_count_at_start,
                "coverage_incomplete": coverage_incomplete,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_verification_watch_item import (
            OperationWorkflowReadinessVerificationWatchItem,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = d.pop("revision")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        state_revision = d.pop("state_revision")

        contract_version = d.pop("contract_version")

        pending_count_at_start = d.pop("pending_count_at_start")

        coverage_incomplete = d.pop("coverage_incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessVerificationWatchItem.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_readiness_verification_watch = cls(
            id=id,
            revision=revision,
            started_at=started_at,
            evaluated_at=evaluated_at,
            state_revision=state_revision,
            contract_version=contract_version,
            pending_count_at_start=pending_count_at_start,
            coverage_incomplete=coverage_incomplete,
            items=items,
        )

        return operation_workflow_readiness_verification_watch
