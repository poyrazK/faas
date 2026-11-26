from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_receiver_credentials import APISandboxReceiverCredentials
from ...models.api_sandbox_receiver_url import APISandboxReceiverURL
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    run: UUID,
    *,
    body: APISandboxReceiverURL,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/api-sandbox-runs/{run}/receiver-invite".format(
            run=quote(str(run), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxReceiverCredentials | Problem:
    if response.status_code == 200:
        response_200 = APISandboxReceiverCredentials.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxReceiverCredentials | Problem]:
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
    body: APISandboxReceiverURL,
) -> Response[APISandboxReceiverCredentials | Problem]:
    """Verify the partner receiver and redeem the invitation.

     Use the one-time invite as the bearer token. The platform makes a redirect-free HTTPS GET to the
    submitted URL. Successful verification consumes the invite and returns the run-scoped signing secret
    and outcome token once.

    Args:
        run (UUID):
        body (APISandboxReceiverURL): Callback endpoint submitted by the partner for an ownership
            check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxReceiverCredentials | Problem]
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
    body: APISandboxReceiverURL,
) -> APISandboxReceiverCredentials | Problem | None:
    """Verify the partner receiver and redeem the invitation.

     Use the one-time invite as the bearer token. The platform makes a redirect-free HTTPS GET to the
    submitted URL. Successful verification consumes the invite and returns the run-scoped signing secret
    and outcome token once.

    Args:
        run (UUID):
        body (APISandboxReceiverURL): Callback endpoint submitted by the partner for an ownership
            check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxReceiverCredentials | Problem
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
    body: APISandboxReceiverURL,
) -> Response[APISandboxReceiverCredentials | Problem]:
    """Verify the partner receiver and redeem the invitation.

     Use the one-time invite as the bearer token. The platform makes a redirect-free HTTPS GET to the
    submitted URL. Successful verification consumes the invite and returns the run-scoped signing secret
    and outcome token once.

    Args:
        run (UUID):
        body (APISandboxReceiverURL): Callback endpoint submitted by the partner for an ownership
            check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxReceiverCredentials | Problem]
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
    body: APISandboxReceiverURL,
) -> APISandboxReceiverCredentials | Problem | None:
    """Verify the partner receiver and redeem the invitation.

     Use the one-time invite as the bearer token. The platform makes a redirect-free HTTPS GET to the
    submitted URL. Successful verification consumes the invite and returns the run-scoped signing secret
    and outcome token once.

    Args:
        run (UUID):
        body (APISandboxReceiverURL): Callback endpoint submitted by the partner for an ownership
            check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxReceiverCredentials | Problem
    """

    return (
        await asyncio_detailed(
            run=run,
            client=client,
            body=body,
        )
    ).parsed
