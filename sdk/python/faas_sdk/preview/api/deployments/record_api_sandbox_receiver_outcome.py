from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_receiver_outcome import APISandboxReceiverOutcome
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    run: UUID,
    *,
    body: APISandboxReceiverOutcome,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/api-sandbox-runs/{run}/receiver-outcome".format(
            run=quote(str(run), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    body: APISandboxReceiverOutcome,
) -> Response[Any | Problem]:
    """Submit the processor outcome for the retried delivery.

     Use the outcome token as the bearer token. Submit the count of committed business effects for the
    stable delivery ID after the second attempt. A 409 can indicate the retry has yet to be recorded or
    the outcome window has closed. A repeat of the same accepted outcome is idempotent while the token
    is active.

    Args:
        run (UUID):
        body (APISandboxReceiverOutcome): Partner-reported result of processing the retried
            sandbox callback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        run=run,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run: UUID,
    *,
    client: AuthenticatedClient,
    body: APISandboxReceiverOutcome,
) -> Any | Problem | None:
    """Submit the processor outcome for the retried delivery.

     Use the outcome token as the bearer token. Submit the count of committed business effects for the
    stable delivery ID after the second attempt. A 409 can indicate the retry has yet to be recorded or
    the outcome window has closed. A repeat of the same accepted outcome is idempotent while the token
    is active.

    Args:
        run (UUID):
        body (APISandboxReceiverOutcome): Partner-reported result of processing the retried
            sandbox callback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        run=run,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    run: UUID,
    *,
    client: AuthenticatedClient,
    body: APISandboxReceiverOutcome,
) -> Response[Any | Problem]:
    """Submit the processor outcome for the retried delivery.

     Use the outcome token as the bearer token. Submit the count of committed business effects for the
    stable delivery ID after the second attempt. A 409 can indicate the retry has yet to be recorded or
    the outcome window has closed. A repeat of the same accepted outcome is idempotent while the token
    is active.

    Args:
        run (UUID):
        body (APISandboxReceiverOutcome): Partner-reported result of processing the retried
            sandbox callback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        run=run,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run: UUID,
    *,
    client: AuthenticatedClient,
    body: APISandboxReceiverOutcome,
) -> Any | Problem | None:
    """Submit the processor outcome for the retried delivery.

     Use the outcome token as the bearer token. Submit the count of committed business effects for the
    stable delivery ID after the second attempt. A 409 can indicate the retry has yet to be recorded or
    the outcome window has closed. A repeat of the same accepted outcome is idempotent while the token
    is active.

    Args:
        run (UUID):
        body (APISandboxReceiverOutcome): Partner-reported result of processing the retried
            sandbox callback.

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
            body=body,
        )
    ).parsed
