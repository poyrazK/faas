from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.update_outbound_binding_policy_request import UpdateOutboundBindingPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    integration: UUID,
    *,
    body: UpdateOutboundBindingPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/outbound-bindings/{integration}".format(
            slug=quote(str(slug), safe=""),
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
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOutboundBindingPolicyRequest,
) -> Response[Any | Problem]:
    """Narrow an app's managed outbound route policy.

     Methods and paths must remain within the operator-approved integration policy. Requires MFA and
    deploy-write scope.

    Args:
        slug (str):
        integration (UUID):
        body (UpdateOutboundBindingPolicyRequest): Explicit nonempty method and path subsets for a
            bound app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        integration=integration,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOutboundBindingPolicyRequest,
) -> Any | Problem | None:
    """Narrow an app's managed outbound route policy.

     Methods and paths must remain within the operator-approved integration policy. Requires MFA and
    deploy-write scope.

    Args:
        slug (str):
        integration (UUID):
        body (UpdateOutboundBindingPolicyRequest): Explicit nonempty method and path subsets for a
            bound app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        integration=integration,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOutboundBindingPolicyRequest,
) -> Response[Any | Problem]:
    """Narrow an app's managed outbound route policy.

     Methods and paths must remain within the operator-approved integration policy. Requires MFA and
    deploy-write scope.

    Args:
        slug (str):
        integration (UUID):
        body (UpdateOutboundBindingPolicyRequest): Explicit nonempty method and path subsets for a
            bound app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        integration=integration,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOutboundBindingPolicyRequest,
) -> Any | Problem | None:
    """Narrow an app's managed outbound route policy.

     Methods and paths must remain within the operator-approved integration policy. Requires MFA and
    deploy-write scope.

    Args:
        slug (str):
        integration (UUID):
        body (UpdateOutboundBindingPolicyRequest): Explicit nonempty method and path subsets for a
            bound app.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            integration=integration,
            client=client,
            body=body,
        )
    ).parsed
