from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_intent_accepted_response_kind import (
    OperatorIntentAcceptedResponseKind,
    check_operator_intent_accepted_response_kind,
)
from ..models.operator_intent_accepted_response_previous_state import (
    OperatorIntentAcceptedResponsePreviousState,
    check_operator_intent_accepted_response_previous_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorIntentAcceptedResponse")


@_attrs_define
class OperatorIntentAcceptedResponse:
    """Wire shape for the 202 Accepted response of POST
    /v1/admin/instances/{id}/force-park, POST
    /v1/admin/instances/{id}/force-restart, and POST
    /v1/admin/apps/{slug}/force-cold-boot (admin scope +
    FAAS_ADMIN_EMAILS allowlist). The audit row is emitted
    under operator.action.{park_instance, restart_instance,
    force_cold_boot} with target_account_id = the instance's /
    app's owning account. StatusURL is the relative path;
    clients prepend the apid base URL. The exact response is retained for
    matching request-key retries independently of target state and audit
    queue retention (ADR-1524).

    """

    ok: bool
    intent_id: UUID
    """Operator intent UUID. Used to poll status_url."""
    status_url: str
    """Relative path to GET /v1/admin/operator-intents/{intent_id}."""
    expires_at: datetime.datetime
    """Advisory horizon for one polling session (UTC, RFC 3339), retained unchanged on replay. This is not receipt
    expiry."""
    kind: OperatorIntentAcceptedResponseKind
    reason: str
    instance_id: UUID | Unset = UNSET
    """Populated for force_park and force_restart. The instance the operator targeted."""
    previous_state: OperatorIntentAcceptedResponsePreviousState | Unset = UNSET
    """Populated for force_park and force_restart. Gate-time read of `instances.state`."""
    app_id: UUID | Unset = UNSET
    """Populated for force_cold_boot. The app whose deployment was targeted."""
    deployment_id: UUID | Unset = UNSET
    """Populated for force_cold_boot. The latest deployment of the app."""
    trace_id: None | str | Unset = UNSET
    """Obs-Meta + Trace-IDs Mega-PR / C4. OTel W3C 32-char
    hex identifier shared with the inbound HTTP request
    and the schedd dispatch context. Always populated for
    the inbound force-action route (the middleware
    generates one when absent); surfaced here so the
    caller can correlate the 202 response with the
    terminal outcome row.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ok = self.ok

        intent_id = str(self.intent_id)

        status_url = self.status_url

        expires_at = self.expires_at.isoformat()

        kind: str = self.kind

        reason = self.reason

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        previous_state: str | Unset = UNSET
        if not isinstance(self.previous_state, Unset):
            previous_state = self.previous_state

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        trace_id: None | str | Unset
        if isinstance(self.trace_id, Unset):
            trace_id = UNSET
        else:
            trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ok": ok,
                "intent_id": intent_id,
                "status_url": status_url,
                "expires_at": expires_at,
                "kind": kind,
                "reason": reason,
            }
        )
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if previous_state is not UNSET:
            field_dict["previous_state"] = previous_state
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ok = d.pop("ok")

        intent_id = UUID(d.pop("intent_id"))

        status_url = d.pop("status_url")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        kind = check_operator_intent_accepted_response_kind(d.pop("kind"))

        reason = d.pop("reason")

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        _previous_state = d.pop("previous_state", UNSET)
        previous_state: OperatorIntentAcceptedResponsePreviousState | Unset
        if isinstance(_previous_state, Unset):
            previous_state = UNSET
        else:
            previous_state = check_operator_intent_accepted_response_previous_state(_previous_state)

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

        def _parse_trace_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        trace_id = _parse_trace_id(d.pop("trace_id", UNSET))

        operator_intent_accepted_response = cls(
            ok=ok,
            intent_id=intent_id,
            status_url=status_url,
            expires_at=expires_at,
            kind=kind,
            reason=reason,
            instance_id=instance_id,
            previous_state=previous_state,
            app_id=app_id,
            deployment_id=deployment_id,
            trace_id=trace_id,
        )

        operator_intent_accepted_response.additional_properties = d
        return operator_intent_accepted_response

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
