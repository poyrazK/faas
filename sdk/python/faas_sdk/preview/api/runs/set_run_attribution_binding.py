from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.run_attribution_binding import RunAttributionBinding
from ...models.run_binding_kind import RunBindingKind
from ...models.set_run_attribution_binding_request import SetRunAttributionBindingRequest
from ...types import Response


def _get_kwargs(
    kind: RunBindingKind,
    id: UUID,
    *,
    body: SetRunAttributionBindingRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/run-controls/bindings/{kind}/{id}".format(
            kind=quote(str(kind), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RunAttributionBinding | None:
    if response.status_code == 200:
        response_200 = RunAttributionBinding.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RunAttributionBinding]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: SetRunAttributionBindingRequest,
) -> Response[Problem | RunAttributionBinding]:
    """Bind a public API key or Agent endpoint to a customer. Key bindings follow rotation.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        body (SetRunAttributionBindingRequest): Customer and optional Agent attribution to assign
            at a specific binding revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RunAttributionBinding]
    """

    kwargs = _get_kwargs(
        kind=kind,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: SetRunAttributionBindingRequest,
) -> Problem | RunAttributionBinding | None:
    """Bind a public API key or Agent endpoint to a customer. Key bindings follow rotation.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        body (SetRunAttributionBindingRequest): Customer and optional Agent attribution to assign
            at a specific binding revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RunAttributionBinding
    """

    return sync_detailed(
        kind=kind,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: SetRunAttributionBindingRequest,
) -> Response[Problem | RunAttributionBinding]:
    """Bind a public API key or Agent endpoint to a customer. Key bindings follow rotation.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        body (SetRunAttributionBindingRequest): Customer and optional Agent attribution to assign
            at a specific binding revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RunAttributionBinding]
    """

    kwargs = _get_kwargs(
        kind=kind,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: SetRunAttributionBindingRequest,
) -> Problem | RunAttributionBinding | None:
    """Bind a public API key or Agent endpoint to a customer. Key bindings follow rotation.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        body (SetRunAttributionBindingRequest): Customer and optional Agent attribution to assign
            at a specific binding revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RunAttributionBinding
    """

    return (
        await asyncio_detailed(
            kind=kind,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
