from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_removal_policy import RouteRemovalPolicy
from ...models.set_route_removal_policy_request import SetRouteRemovalPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetRouteRemovalPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/route-removal/policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteRemovalPolicy | None:
    if response.status_code == 200:
        response_200 = RouteRemovalPolicy.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteRemovalPolicy]:
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
    body: SetRouteRemovalPolicyRequest,
) -> Response[Problem | RouteRemovalPolicy]:
    """Configure server route removal enforcement.

     Requires account admin authorization and completed MFA. expected_revision is mandatory. First
    configuration requires one production baseline at 100 percent, or no production deployments. Policy
    changes invalidate previous approvals. Quiet observation begins when a policy first adopts a
    baseline, resets on full cutover or capture changes, and survives policy mode changes.

    Args:
        slug (str):
        body (SetRouteRemovalPolicyRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteRemovalPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetRouteRemovalPolicyRequest,
) -> Problem | RouteRemovalPolicy | None:
    """Configure server route removal enforcement.

     Requires account admin authorization and completed MFA. expected_revision is mandatory. First
    configuration requires one production baseline at 100 percent, or no production deployments. Policy
    changes invalidate previous approvals. Quiet observation begins when a policy first adopts a
    baseline, resets on full cutover or capture changes, and survives policy mode changes.

    Args:
        slug (str):
        body (SetRouteRemovalPolicyRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteRemovalPolicy
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetRouteRemovalPolicyRequest,
) -> Response[Problem | RouteRemovalPolicy]:
    """Configure server route removal enforcement.

     Requires account admin authorization and completed MFA. expected_revision is mandatory. First
    configuration requires one production baseline at 100 percent, or no production deployments. Policy
    changes invalidate previous approvals. Quiet observation begins when a policy first adopts a
    baseline, resets on full cutover or capture changes, and survives policy mode changes.

    Args:
        slug (str):
        body (SetRouteRemovalPolicyRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteRemovalPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetRouteRemovalPolicyRequest,
) -> Problem | RouteRemovalPolicy | None:
    """Configure server route removal enforcement.

     Requires account admin authorization and completed MFA. expected_revision is mandatory. First
    configuration requires one production baseline at 100 percent, or no production deployments. Policy
    changes invalidate previous approvals. Quiet observation begins when a policy first adopts a
    baseline, resets on full cutover or capture changes, and survives policy mode changes.

    Args:
        slug (str):
        body (SetRouteRemovalPolicyRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteRemovalPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
