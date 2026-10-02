from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.put_outbound_runs_binding_request import PutOutboundRunsBindingRequest
from ...types import Response


def _get_kwargs(
    integration: UUID,
    *,
    body: PutOutboundRunsBindingRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/integrations/{integration}/runs".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundRunsBindingRequest,
) -> Response[Any | Problem]:
    """Grant or revoke use of a managed integration by stateless Runs.

     Requires MFA and deploy-write scope. Grants are separate from app bindings and require an enabled
    integration with a configured managed credential and at least one allowed route. Run tool calls are
    available only after the Runs broker is enabled for the deployment.

    Args:
        integration (UUID):
        body (PutOutboundRunsBindingRequest): Explicit account decision to allow or deny stateless
            Runs access to an integration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundRunsBindingRequest,
) -> Any | Problem | None:
    """Grant or revoke use of a managed integration by stateless Runs.

     Requires MFA and deploy-write scope. Grants are separate from app bindings and require an enabled
    integration with a configured managed credential and at least one allowed route. Run tool calls are
    available only after the Runs broker is enabled for the deployment.

    Args:
        integration (UUID):
        body (PutOutboundRunsBindingRequest): Explicit account decision to allow or deny stateless
            Runs access to an integration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundRunsBindingRequest,
) -> Response[Any | Problem]:
    """Grant or revoke use of a managed integration by stateless Runs.

     Requires MFA and deploy-write scope. Grants are separate from app bindings and require an enabled
    integration with a configured managed credential and at least one allowed route. Run tool calls are
    available only after the Runs broker is enabled for the deployment.

    Args:
        integration (UUID):
        body (PutOutboundRunsBindingRequest): Explicit account decision to allow or deny stateless
            Runs access to an integration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundRunsBindingRequest,
) -> Any | Problem | None:
    """Grant or revoke use of a managed integration by stateless Runs.

     Requires MFA and deploy-write scope. Grants are separate from app bindings and require an enabled
    integration with a configured managed credential and at least one allowed route. Run tool calls are
    available only after the Runs broker is enabled for the deployment.

    Args:
        integration (UUID):
        body (PutOutboundRunsBindingRequest): Explicit account decision to allow or deny stateless
            Runs access to an integration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            body=body,
        )
    ).parsed
