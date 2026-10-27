from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_policy_plan import RoutePolicyPlan
from ...models.route_policy_plan_request import RoutePolicyPlanRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: RoutePolicyPlanRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/route-policy/plan".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RoutePolicyPlan | None:
    if response.status_code == 200:
        response_200 = RoutePolicyPlan.from_dict(response.json())

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
) -> Response[Problem | RoutePolicyPlan]:
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
    body: RoutePolicyPlanRequest,
) -> Response[Problem | RoutePolicyPlan]:
    """plan route policy.

     Read a consistent app policy snapshot and propose throttle and budget changes for concrete requests
    or captured route groups. Group plans bind an app-owned captured deployment contract and report full
    inventory impact. Requires apps:read or admin and completed MFA. This POST only reads configuration.

    Args:
        slug (str):
        body (RoutePolicyPlanRequest): Supply exactly one source: inline requirements or
            saved=true. Saved mode reads current app intent in the same snapshot as policy and
            capture; deployment_id is required. expected_revision optionally pins saved planning.
            Explicit burst choice is required for creating throttles without a selected policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePolicyPlan]
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
    body: RoutePolicyPlanRequest,
) -> Problem | RoutePolicyPlan | None:
    """plan route policy.

     Read a consistent app policy snapshot and propose throttle and budget changes for concrete requests
    or captured route groups. Group plans bind an app-owned captured deployment contract and report full
    inventory impact. Requires apps:read or admin and completed MFA. This POST only reads configuration.

    Args:
        slug (str):
        body (RoutePolicyPlanRequest): Supply exactly one source: inline requirements or
            saved=true. Saved mode reads current app intent in the same snapshot as policy and
            capture; deployment_id is required. expected_revision optionally pins saved planning.
            Explicit burst choice is required for creating throttles without a selected policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePolicyPlan
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
    body: RoutePolicyPlanRequest,
) -> Response[Problem | RoutePolicyPlan]:
    """plan route policy.

     Read a consistent app policy snapshot and propose throttle and budget changes for concrete requests
    or captured route groups. Group plans bind an app-owned captured deployment contract and report full
    inventory impact. Requires apps:read or admin and completed MFA. This POST only reads configuration.

    Args:
        slug (str):
        body (RoutePolicyPlanRequest): Supply exactly one source: inline requirements or
            saved=true. Saved mode reads current app intent in the same snapshot as policy and
            capture; deployment_id is required. expected_revision optionally pins saved planning.
            Explicit burst choice is required for creating throttles without a selected policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePolicyPlan]
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
    body: RoutePolicyPlanRequest,
) -> Problem | RoutePolicyPlan | None:
    """plan route policy.

     Read a consistent app policy snapshot and propose throttle and budget changes for concrete requests
    or captured route groups. Group plans bind an app-owned captured deployment contract and report full
    inventory impact. Requires apps:read or admin and completed MFA. This POST only reads configuration.

    Args:
        slug (str):
        body (RoutePolicyPlanRequest): Supply exactly one source: inline requirements or
            saved=true. Saved mode reads current app intent in the same snapshot as policy and
            capture; deployment_id is required. expected_revision optionally pins saved planning.
            Explicit burst choice is required for creating throttles without a selected policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePolicyPlan
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
