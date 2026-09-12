from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.admin_status_incident_create_request import AdminStatusIncidentCreateRequest
from ...models.admin_status_maintenance_create_request import AdminStatusMaintenanceCreateRequest
from ...models.problem import Problem
from ...models.public_status_event import PublicStatusEvent
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest,
    idempotency_key: str,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/status/incidents",
        "cookies": cookies,
    }

    if isinstance(body, AdminStatusIncidentCreateRequest):
        _kwargs["json"] = body.to_dict()
    else:
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | PublicStatusEvent | None:
    if response.status_code == 201:
        response_201 = PublicStatusEvent.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | PublicStatusEvent]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest,
    idempotency_key: str,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | PublicStatusEvent]:
    """Publish a public incident or maintenance event (admin-only).

     Requires admin scope, operator allowlist membership, MFA, recent step-up authentication, and an
    Idempotency-Key.

    Args:
        idempotency_key (str):
        faas_sid (str | Unset):
        body (AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest): Operator
            request to publish an incident or schedule maintenance, discriminated by kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublicStatusEvent]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest,
    idempotency_key: str,
    faas_sid: str | Unset = UNSET,
) -> Problem | PublicStatusEvent | None:
    """Publish a public incident or maintenance event (admin-only).

     Requires admin scope, operator allowlist membership, MFA, recent step-up authentication, and an
    Idempotency-Key.

    Args:
        idempotency_key (str):
        faas_sid (str | Unset):
        body (AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest): Operator
            request to publish an incident or schedule maintenance, discriminated by kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublicStatusEvent
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest,
    idempotency_key: str,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | PublicStatusEvent]:
    """Publish a public incident or maintenance event (admin-only).

     Requires admin scope, operator allowlist membership, MFA, recent step-up authentication, and an
    Idempotency-Key.

    Args:
        idempotency_key (str):
        faas_sid (str | Unset):
        body (AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest): Operator
            request to publish an incident or schedule maintenance, discriminated by kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublicStatusEvent]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest,
    idempotency_key: str,
    faas_sid: str | Unset = UNSET,
) -> Problem | PublicStatusEvent | None:
    """Publish a public incident or maintenance event (admin-only).

     Requires admin scope, operator allowlist membership, MFA, recent step-up authentication, and an
    Idempotency-Key.

    Args:
        idempotency_key (str):
        faas_sid (str | Unset):
        body (AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest): Operator
            request to publish an incident or schedule maintenance, discriminated by kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublicStatusEvent
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
            faas_sid=faas_sid,
        )
    ).parsed
