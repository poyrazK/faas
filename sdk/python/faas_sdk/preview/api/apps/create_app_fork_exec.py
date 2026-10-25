from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_fork_exec_response import AppForkExecResponse
from ...models.create_app_fork_exec_request import CreateAppForkExecRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: CreateAppForkExecRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/forks/{id}/execs".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppForkExecResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppForkExecResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppForkExecResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkExecRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppForkExecResponse | Problem]:
    """Run a command inside a running production fork.

     Queues one command to run inside the running fork (ADR-732), in the
    app's working directory and user, with its manifest environment and
    no secrets. The fork cannot reach the network. Poll the returned
    command for its exit code and the tail of its output. A command never
    runs in a serving instance. Needs `secrets:read` as well as
    `deploy:write`; every request is audited (without its arguments).

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkExecRequest): Body of `POST /v1/apps/{slug}/forks/{id}/execs`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppForkExecResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkExecRequest,
    idempotency_key: str | Unset = UNSET,
) -> AppForkExecResponse | Problem | None:
    """Run a command inside a running production fork.

     Queues one command to run inside the running fork (ADR-732), in the
    app's working directory and user, with its manifest environment and
    no secrets. The fork cannot reach the network. Poll the returned
    command for its exit code and the tail of its output. A command never
    runs in a serving instance. Needs `secrets:read` as well as
    `deploy:write`; every request is audited (without its arguments).

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkExecRequest): Body of `POST /v1/apps/{slug}/forks/{id}/execs`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppForkExecResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkExecRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppForkExecResponse | Problem]:
    """Run a command inside a running production fork.

     Queues one command to run inside the running fork (ADR-732), in the
    app's working directory and user, with its manifest environment and
    no secrets. The fork cannot reach the network. Poll the returned
    command for its exit code and the tail of its output. A command never
    runs in a serving instance. Needs `secrets:read` as well as
    `deploy:write`; every request is audited (without its arguments).

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkExecRequest): Body of `POST /v1/apps/{slug}/forks/{id}/execs`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppForkExecResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkExecRequest,
    idempotency_key: str | Unset = UNSET,
) -> AppForkExecResponse | Problem | None:
    """Run a command inside a running production fork.

     Queues one command to run inside the running fork (ADR-732), in the
    app's working directory and user, with its manifest environment and
    no secrets. The fork cannot reach the network. Poll the returned
    command for its exit code and the tail of its output. A command never
    runs in a serving instance. Needs `secrets:read` as well as
    `deploy:write`; every request is audited (without its arguments).

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkExecRequest): Body of `POST /v1/apps/{slug}/forks/{id}/execs`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppForkExecResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
