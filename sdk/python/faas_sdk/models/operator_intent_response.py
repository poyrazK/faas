from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_intent_response_kind import OperatorIntentResponseKind, check_operator_intent_response_kind
from ..models.operator_intent_response_status import OperatorIntentResponseStatus, check_operator_intent_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operator_intent_response_metadata import OperatorIntentResponseMetadata


T = TypeVar("T", bound="OperatorIntentResponse")


@_attrs_define
class OperatorIntentResponse:
    """Wire shape for GET /v1/admin/operator-intents/{id}
    (admin scope + FAAS_ADMIN_EMAILS allowlist; NO MFA —
    mirrors getFireCronRequest). IDOR closure: 404 (not 403)
    on wrong-owner so an admin cannot distinguish "wrong id"
    from "wrong owner".

    """

    intent_id: UUID
    kind: OperatorIntentResponseKind
    status: OperatorIntentResponseStatus
    target_id: str
    """Instance UUID (force_park or force_restart), deployment UUID (force_cold_boot), or compute-node UUID (node
    lifecycle intents)."""
    actor_id: UUID
    """Admin account that requested the operation."""
    reason: str
    """Bounded operator-supplied reason recorded with the intent."""
    metadata: OperatorIntentResponseMetadata
    """Kind-specific preflight and desired-state metadata captured before dispatch."""
    requested_at: datetime.datetime
    account_id: UUID | Unset = UNSET
    """Owning account. Omitted for fleet-level node lifecycle intents."""
    started_at: datetime.datetime | Unset = UNSET
    """Set when schedd claims the intent (pending → running)."""
    finished_at: datetime.datetime | Unset = UNSET
    """Set on terminal status (succeeded/failed/cancelled)."""
    error: str | Unset = UNSET
    """Bounded dispatch error message (1 KB cap) on failed status."""
    snap_ids_marked_stale: list[UUID] | Unset = UNSET
    """Populated for force_cold_boot and force_restart on succeeded status. Empty when no snapshots existed."""
    trace_id: None | str | Unset = UNSET
    """Obs-Meta + Trace-IDs Mega-PR / C4. OTel W3C 32-char
    hex identifier shared with the inbound HTTP request
    and the schedd dispatch context. NULL when the row
    predates C4 or when the inbound request carried no
    trace_id (e.g. cron-fired reaper paths). Joins
    "this alert" ↔ "this operator action" ↔ "this
    schedd dispatch" on one column.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        intent_id = str(self.intent_id)

        kind: str = self.kind

        status: str = self.status

        target_id = self.target_id

        actor_id = str(self.actor_id)

        reason = self.reason

        metadata = self.metadata.to_dict()

        requested_at = self.requested_at.isoformat()

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        error = self.error

        snap_ids_marked_stale: list[str] | Unset = UNSET
        if not isinstance(self.snap_ids_marked_stale, Unset):
            snap_ids_marked_stale = []
            for snap_ids_marked_stale_item_data in self.snap_ids_marked_stale:
                snap_ids_marked_stale_item = str(snap_ids_marked_stale_item_data)
                snap_ids_marked_stale.append(snap_ids_marked_stale_item)

        trace_id: None | str | Unset
        if isinstance(self.trace_id, Unset):
            trace_id = UNSET
        else:
            trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "intent_id": intent_id,
                "kind": kind,
                "status": status,
                "target_id": target_id,
                "actor_id": actor_id,
                "reason": reason,
                "metadata": metadata,
                "requested_at": requested_at,
            }
        )
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if error is not UNSET:
            field_dict["error"] = error
        if snap_ids_marked_stale is not UNSET:
            field_dict["snap_ids_marked_stale"] = snap_ids_marked_stale
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_intent_response_metadata import OperatorIntentResponseMetadata

        d = dict(src_dict)
        intent_id = UUID(d.pop("intent_id"))

        kind = check_operator_intent_response_kind(d.pop("kind"))

        status = check_operator_intent_response_status(d.pop("status"))

        target_id = d.pop("target_id")

        actor_id = UUID(d.pop("actor_id"))

        reason = d.pop("reason")

        metadata = OperatorIntentResponseMetadata.from_dict(d.pop("metadata"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        error = d.pop("error", UNSET)

        _snap_ids_marked_stale = d.pop("snap_ids_marked_stale", UNSET)
        snap_ids_marked_stale: list[UUID] | Unset = UNSET
        if _snap_ids_marked_stale is not UNSET:
            snap_ids_marked_stale = []
            for snap_ids_marked_stale_item_data in _snap_ids_marked_stale:
                snap_ids_marked_stale_item = UUID(snap_ids_marked_stale_item_data)

                snap_ids_marked_stale.append(snap_ids_marked_stale_item)

        def _parse_trace_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        trace_id = _parse_trace_id(d.pop("trace_id", UNSET))

        operator_intent_response = cls(
            intent_id=intent_id,
            kind=kind,
            status=status,
            target_id=target_id,
            actor_id=actor_id,
            reason=reason,
            metadata=metadata,
            requested_at=requested_at,
            account_id=account_id,
            started_at=started_at,
            finished_at=finished_at,
            error=error,
            snap_ids_marked_stale=snap_ids_marked_stale,
            trace_id=trace_id,
        )

        operator_intent_response.additional_properties = d
        return operator_intent_response

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
