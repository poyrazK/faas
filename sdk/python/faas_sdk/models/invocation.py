from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.invocation_source import InvocationSource, check_invocation_source
from ..models.invocation_state import InvocationState, check_invocation_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.invocation_headers import InvocationHeaders
    from ..models.invocation_payload import InvocationPayload
    from ..models.invocation_result_type_0 import InvocationResultType0
    from ..models.invocation_retry_policy_type_0 import InvocationRetryPolicyType0


T = TypeVar("T", bound="Invocation")


@_attrs_define
class Invocation:
    """A single invocations row. Account-scoped; cross-account reads return 404."""

    id: str
    app_id: str
    account_id: str
    source: InvocationSource
    state: InvocationState
    created_at: datetime.datetime
    method: str | Unset = UNSET
    path: str | Unset = UNSET
    payload: InvocationPayload | Unset = UNSET
    headers: InvocationHeaders | Unset = UNSET
    scheduled_at: datetime.datetime | None | Unset = UNSET
    due_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET
    instance_id: None | str | Unset = UNSET
    result: InvocationResultType0 | None | Unset = UNSET
    last_error: None | str | Unset = UNSET
    ack_url: None | str | Unset = UNSET
    """Optional ack URL for queueReceive consumers; populated on queue-sourced rows."""
    attempts: int | Unset = UNSET
    """Number of dispatch attempts so far; 0 on the first try."""
    lease_expires_at: datetime.datetime | None | Unset = UNSET
    """When the in-flight dispatch lease expires; null when no lease is held."""
    received_at: datetime.datetime | None | Unset = UNSET
    """When the drain first claimed the row; null until claimed."""
    deadline_at: datetime.datetime | None | Unset = UNSET
    """ADR-134 PR-B. Optional hard-stop. Drain transitions the row to dead_letter when this time passes while still
    pending|dispatching."""
    retry_policy: InvocationRetryPolicyType0 | None | Unset = UNSET
    """ADR-134 PR-B. Optional per-row retry curve override; decodes into dispatch.RetryPolicy (max_attempts,
    base_seconds, max_seconds, jitter_seconds)."""
    result_retention_until: datetime.datetime | None | Unset = UNSET
    """ADR-134 PR-B. Optional explicit retention horizon. NULL means 'use plan default'
    (Limits.MaxAsyncResultRetentionSeconds)."""
    last_replayed_at: datetime.datetime | None | Unset = UNSET
    """ADR-134 PR-C. When this row was most recently replayed from dead_letter via POST
    /v1/apps/{slug}/queues/dead_letter/{id}/replay. NULL until first replay."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.invocation_result_type_0 import InvocationResultType0
        from ..models.invocation_retry_policy_type_0 import InvocationRetryPolicyType0

        id = self.id

        app_id = self.app_id

        account_id = self.account_id

        source: str = self.source

        state: str = self.state

        created_at = self.created_at.isoformat()

        method = self.method

        path = self.path

        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        scheduled_at: None | str | Unset
        if isinstance(self.scheduled_at, Unset):
            scheduled_at = UNSET
        elif isinstance(self.scheduled_at, datetime.datetime):
            scheduled_at = self.scheduled_at.isoformat()
        else:
            scheduled_at = self.scheduled_at

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        instance_id: None | str | Unset
        if isinstance(self.instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = self.instance_id

        result: dict[str, Any] | None | Unset
        if isinstance(self.result, Unset):
            result = UNSET
        elif isinstance(self.result, InvocationResultType0):
            result = self.result.to_dict()
        else:
            result = self.result

        last_error: None | str | Unset
        if isinstance(self.last_error, Unset):
            last_error = UNSET
        else:
            last_error = self.last_error

        ack_url: None | str | Unset
        if isinstance(self.ack_url, Unset):
            ack_url = UNSET
        else:
            ack_url = self.ack_url

        attempts = self.attempts

        lease_expires_at: None | str | Unset
        if isinstance(self.lease_expires_at, Unset):
            lease_expires_at = UNSET
        elif isinstance(self.lease_expires_at, datetime.datetime):
            lease_expires_at = self.lease_expires_at.isoformat()
        else:
            lease_expires_at = self.lease_expires_at

        received_at: None | str | Unset
        if isinstance(self.received_at, Unset):
            received_at = UNSET
        elif isinstance(self.received_at, datetime.datetime):
            received_at = self.received_at.isoformat()
        else:
            received_at = self.received_at

        deadline_at: None | str | Unset
        if isinstance(self.deadline_at, Unset):
            deadline_at = UNSET
        elif isinstance(self.deadline_at, datetime.datetime):
            deadline_at = self.deadline_at.isoformat()
        else:
            deadline_at = self.deadline_at

        retry_policy: dict[str, Any] | None | Unset
        if isinstance(self.retry_policy, Unset):
            retry_policy = UNSET
        elif isinstance(self.retry_policy, InvocationRetryPolicyType0):
            retry_policy = self.retry_policy.to_dict()
        else:
            retry_policy = self.retry_policy

        result_retention_until: None | str | Unset
        if isinstance(self.result_retention_until, Unset):
            result_retention_until = UNSET
        elif isinstance(self.result_retention_until, datetime.datetime):
            result_retention_until = self.result_retention_until.isoformat()
        else:
            result_retention_until = self.result_retention_until

        last_replayed_at: None | str | Unset
        if isinstance(self.last_replayed_at, Unset):
            last_replayed_at = UNSET
        elif isinstance(self.last_replayed_at, datetime.datetime):
            last_replayed_at = self.last_replayed_at.isoformat()
        else:
            last_replayed_at = self.last_replayed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "source": source,
                "state": state,
                "created_at": created_at,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path
        if payload is not UNSET:
            field_dict["payload"] = payload
        if headers is not UNSET:
            field_dict["headers"] = headers
        if scheduled_at is not UNSET:
            field_dict["scheduled_at"] = scheduled_at
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if result is not UNSET:
            field_dict["result"] = result
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if ack_url is not UNSET:
            field_dict["ack_url"] = ack_url
        if attempts is not UNSET:
            field_dict["attempts"] = attempts
        if lease_expires_at is not UNSET:
            field_dict["lease_expires_at"] = lease_expires_at
        if received_at is not UNSET:
            field_dict["received_at"] = received_at
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if result_retention_until is not UNSET:
            field_dict["result_retention_until"] = result_retention_until
        if last_replayed_at is not UNSET:
            field_dict["last_replayed_at"] = last_replayed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.invocation_headers import InvocationHeaders
        from ..models.invocation_payload import InvocationPayload
        from ..models.invocation_result_type_0 import InvocationResultType0
        from ..models.invocation_retry_policy_type_0 import InvocationRetryPolicyType0

        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        account_id = d.pop("account_id")

        source = check_invocation_source(d.pop("source"))

        state = check_invocation_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        _payload = d.pop("payload", UNSET)
        payload: InvocationPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = InvocationPayload.from_dict(_payload)

        _headers = d.pop("headers", UNSET)
        headers: InvocationHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = InvocationHeaders.from_dict(_headers)

        def _parse_scheduled_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                scheduled_at_type_0 = datetime.datetime.fromisoformat(data)

                return scheduled_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        scheduled_at = _parse_scheduled_at(d.pop("scheduled_at", UNSET))

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        def _parse_instance_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        instance_id = _parse_instance_id(d.pop("instance_id", UNSET))

        def _parse_result(data: object) -> InvocationResultType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                result_type_0 = InvocationResultType0.from_dict(data)

                return result_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(InvocationResultType0 | None | Unset, data)

        result = _parse_result(d.pop("result", UNSET))

        def _parse_last_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        last_error = _parse_last_error(d.pop("last_error", UNSET))

        def _parse_ack_url(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        ack_url = _parse_ack_url(d.pop("ack_url", UNSET))

        attempts = d.pop("attempts", UNSET)

        def _parse_lease_expires_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                lease_expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return lease_expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        lease_expires_at = _parse_lease_expires_at(d.pop("lease_expires_at", UNSET))

        def _parse_received_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                received_at_type_0 = datetime.datetime.fromisoformat(data)

                return received_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        received_at = _parse_received_at(d.pop("received_at", UNSET))

        def _parse_deadline_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deadline_at_type_0 = datetime.datetime.fromisoformat(data)

                return deadline_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        deadline_at = _parse_deadline_at(d.pop("deadline_at", UNSET))

        def _parse_retry_policy(data: object) -> InvocationRetryPolicyType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                retry_policy_type_0 = InvocationRetryPolicyType0.from_dict(data)

                return retry_policy_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(InvocationRetryPolicyType0 | None | Unset, data)

        retry_policy = _parse_retry_policy(d.pop("retry_policy", UNSET))

        def _parse_result_retention_until(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                result_retention_until_type_0 = datetime.datetime.fromisoformat(data)

                return result_retention_until_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        result_retention_until = _parse_result_retention_until(d.pop("result_retention_until", UNSET))

        def _parse_last_replayed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_replayed_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_replayed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_replayed_at = _parse_last_replayed_at(d.pop("last_replayed_at", UNSET))

        invocation = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            source=source,
            state=state,
            created_at=created_at,
            method=method,
            path=path,
            payload=payload,
            headers=headers,
            scheduled_at=scheduled_at,
            due_at=due_at,
            completed_at=completed_at,
            instance_id=instance_id,
            result=result,
            last_error=last_error,
            ack_url=ack_url,
            attempts=attempts,
            lease_expires_at=lease_expires_at,
            received_at=received_at,
            deadline_at=deadline_at,
            retry_policy=retry_policy,
            result_retention_until=result_retention_until,
            last_replayed_at=last_replayed_at,
        )

        invocation.additional_properties = d
        return invocation

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
