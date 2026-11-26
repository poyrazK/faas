from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/api-sandbox-runs/{run}/receiver-ready".format(
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem]:
    """Confirm the partner is ready to receive the callback.

     Use the outcome token as the bearer token after installing the run signing secret in the test
    receiver.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        run=run,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | None:
    """Confirm the partner is ready to receive the callback.

     Use the outcome token as the bearer token after installing the run signing secret in the test
    receiver.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem]:
    """Confirm the partner is ready to receive the callback.

     Use the outcome token as the bearer token after installing the run signing secret in the test
    receiver.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        run=run,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | None:
    """Confirm the partner is ready to receive the callback.

     Use the outcome token as the bearer token after installing the run signing secret in the test
    receiver.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            run=run,
            client=client,
        )
    ).parsed
