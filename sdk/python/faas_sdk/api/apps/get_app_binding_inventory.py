from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_binding_inventory import AppBindingInventory
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    deployment_id: UUID | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_deployment_id: str | Unset = UNSET
    if not isinstance(deployment_id, Unset):
        json_deployment_id = str(deployment_id)
    params["deployment_id"] = json_deployment_id

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/bindings".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppBindingInventory | Problem | None:
    if response.status_code == 200:
        response_200 = AppBindingInventory.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppBindingInventory | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> Response[AppBindingInventory | Problem]:
    """Inspect all runtime resource bindings attached to an app.

     Read-only, best-effort metadata for service, PostgreSQL, object-storage,
    queue and outbound bindings. Requires apps:read or admin. PostgreSQL
    sections additionally require managed-postgres:read or admin; object
    storage sections require storage:manage or admin, matching the existing
    compute-binding read surface. MFA-pending sessions are rejected.
    Missing permissions and failed sections appear as structured issues in
    a 200 response with complete=false; successfully read sections remain.
    An unconfigured managed PostgreSQL feature is a warning. Other issues
    have error severity. No provider calls, workload probes or writes occur.
    State describes configuration/provisioning, not applied runtime health.
    Runtime status is unknown unless a scheduler queue observation exists;
    verification status includes durable service, PostgreSQL and object-storage task-guest canary
    results. GeneratedAt is collection time, not a
    promise of an atomic snapshot. Credential material and raw errors are
    excluded. Responses use Cache-Control: no-store.

    Args:
        slug (str):
        deployment_id (UUID | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppBindingInventory | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> AppBindingInventory | Problem | None:
    """Inspect all runtime resource bindings attached to an app.

     Read-only, best-effort metadata for service, PostgreSQL, object-storage,
    queue and outbound bindings. Requires apps:read or admin. PostgreSQL
    sections additionally require managed-postgres:read or admin; object
    storage sections require storage:manage or admin, matching the existing
    compute-binding read surface. MFA-pending sessions are rejected.
    Missing permissions and failed sections appear as structured issues in
    a 200 response with complete=false; successfully read sections remain.
    An unconfigured managed PostgreSQL feature is a warning. Other issues
    have error severity. No provider calls, workload probes or writes occur.
    State describes configuration/provisioning, not applied runtime health.
    Runtime status is unknown unless a scheduler queue observation exists;
    verification status includes durable service, PostgreSQL and object-storage task-guest canary
    results. GeneratedAt is collection time, not a
    promise of an atomic snapshot. Credential material and raw errors are
    excluded. Responses use Cache-Control: no-store.

    Args:
        slug (str):
        deployment_id (UUID | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppBindingInventory | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        deployment_id=deployment_id,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> Response[AppBindingInventory | Problem]:
    """Inspect all runtime resource bindings attached to an app.

     Read-only, best-effort metadata for service, PostgreSQL, object-storage,
    queue and outbound bindings. Requires apps:read or admin. PostgreSQL
    sections additionally require managed-postgres:read or admin; object
    storage sections require storage:manage or admin, matching the existing
    compute-binding read surface. MFA-pending sessions are rejected.
    Missing permissions and failed sections appear as structured issues in
    a 200 response with complete=false; successfully read sections remain.
    An unconfigured managed PostgreSQL feature is a warning. Other issues
    have error severity. No provider calls, workload probes or writes occur.
    State describes configuration/provisioning, not applied runtime health.
    Runtime status is unknown unless a scheduler queue observation exists;
    verification status includes durable service, PostgreSQL and object-storage task-guest canary
    results. GeneratedAt is collection time, not a
    promise of an atomic snapshot. Credential material and raw errors are
    excluded. Responses use Cache-Control: no-store.

    Args:
        slug (str):
        deployment_id (UUID | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppBindingInventory | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> AppBindingInventory | Problem | None:
    """Inspect all runtime resource bindings attached to an app.

     Read-only, best-effort metadata for service, PostgreSQL, object-storage,
    queue and outbound bindings. Requires apps:read or admin. PostgreSQL
    sections additionally require managed-postgres:read or admin; object
    storage sections require storage:manage or admin, matching the existing
    compute-binding read surface. MFA-pending sessions are rejected.
    Missing permissions and failed sections appear as structured issues in
    a 200 response with complete=false; successfully read sections remain.
    An unconfigured managed PostgreSQL feature is a warning. Other issues
    have error severity. No provider calls, workload probes or writes occur.
    State describes configuration/provisioning, not applied runtime health.
    Runtime status is unknown unless a scheduler queue observation exists;
    verification status includes durable service, PostgreSQL and object-storage task-guest canary
    results. GeneratedAt is collection time, not a
    promise of an atomic snapshot. Credential material and raw errors are
    excluded. Responses use Cache-Control: no-store.

    Args:
        slug (str):
        deployment_id (UUID | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppBindingInventory | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            deployment_id=deployment_id,
            scope=scope,
        )
    ).parsed
