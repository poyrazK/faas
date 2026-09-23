from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_task_response import AppTaskResponse
from ...models.create_app_task_request import CreateAppTaskRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateAppTaskRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/tasks".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppTaskResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppTaskResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[AppTaskResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateAppTaskRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppTaskResponse | Problem]:
    """Run a one-off command against an app deployment.

     Queues one manual command in a fresh task VM using the app's current
    live deployment, scoped configuration, bindings, and network policy.
    The selected deployment is pinned at admission and the VM is destroyed
    before terminal completion. Release tasks cannot be created here.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAppTaskRequest): One manual command to execute against the app's live
            deployment.
            `command_shell=false` executes argv directly. Shell mode requires one
            command string and is explicit so clients preserve quoting semantics.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateAppTaskRequest,
    idempotency_key: str | Unset = UNSET,
) -> AppTaskResponse | Problem | None:
    """Run a one-off command against an app deployment.

     Queues one manual command in a fresh task VM using the app's current
    live deployment, scoped configuration, bindings, and network policy.
    The selected deployment is pinned at admission and the VM is destroyed
    before terminal completion. Release tasks cannot be created here.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAppTaskRequest): One manual command to execute against the app's live
            deployment.
            `command_shell=false` executes argv directly. Shell mode requires one
            command string and is explicit so clients preserve quoting semantics.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateAppTaskRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppTaskResponse | Problem]:
    """Run a one-off command against an app deployment.

     Queues one manual command in a fresh task VM using the app's current
    live deployment, scoped configuration, bindings, and network policy.
    The selected deployment is pinned at admission and the VM is destroyed
    before terminal completion. Release tasks cannot be created here.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAppTaskRequest): One manual command to execute against the app's live
            deployment.
            `command_shell=false` executes argv directly. Shell mode requires one
            command string and is explicit so clients preserve quoting semantics.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateAppTaskRequest,
    idempotency_key: str | Unset = UNSET,
) -> AppTaskResponse | Problem | None:
    """Run a one-off command against an app deployment.

     Queues one manual command in a fresh task VM using the app's current
    live deployment, scoped configuration, bindings, and network policy.
    The selected deployment is pinned at admission and the VM is destroyed
    before terminal completion. Release tasks cannot be created here.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAppTaskRequest): One manual command to execute against the app's live
            deployment.
            `command_shell=false` executes argv directly. Shell mode requires one
            command string and is explicit so clients preserve quoting semantics.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
