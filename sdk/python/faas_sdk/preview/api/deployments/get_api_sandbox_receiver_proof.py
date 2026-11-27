from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_receiver_proof import APISandboxReceiverProof
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/api-sandbox-runs/{run}/receiver-invite".format(
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxReceiverProof | Problem:
    if response.status_code == 200:
        response_200 = APISandboxReceiverProof.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxReceiverProof | Problem]:
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
) -> Response[APISandboxReceiverProof | Problem]:
    """Read the endpoint ownership proof.

     Use the one-time receiver invite as the bearer token. Configure the exact HTTPS callback URL to
    return the proof as the GET response body.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxReceiverProof | Problem]
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
) -> APISandboxReceiverProof | Problem | None:
    """Read the endpoint ownership proof.

     Use the one-time receiver invite as the bearer token. Configure the exact HTTPS callback URL to
    return the proof as the GET response body.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxReceiverProof | Problem
    """

    return sync_detailed(
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[APISandboxReceiverProof | Problem]:
    """Read the endpoint ownership proof.

     Use the one-time receiver invite as the bearer token. Configure the exact HTTPS callback URL to
    return the proof as the GET response body.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxReceiverProof | Problem]
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
) -> APISandboxReceiverProof | Problem | None:
    """Read the endpoint ownership proof.

     Use the one-time receiver invite as the bearer token. Configure the exact HTTPS callback URL to
    return the proof as the GET response body.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxReceiverProof | Problem
    """

    return (
        await asyncio_detailed(
            run=run,
            client=client,
        )
    ).parsed
