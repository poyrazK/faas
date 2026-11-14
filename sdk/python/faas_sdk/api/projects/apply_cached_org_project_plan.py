from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_cached_project_plan_request import ApplyCachedProjectPlanRequest
from ...models.apply_response import ApplyResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    *,
    body: ApplyCachedProjectPlanRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/projects/apply-plan".format(
            organization=quote(str(organization), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
) -> Response[ApplyResponse | Problem]:
    """Deploy a company project's reviewed source without another upload.

     Applies the exact archive retained by a company project scan in the same organization. A plan
    reviewed for a personal project or another organization is refused. The normal scanner and apply
    admission gates run again; the reviewed source expires after one hour. Requires the deploy role.

    Args:
        organization (str):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
) -> ApplyResponse | Problem | None:
    """Deploy a company project's reviewed source without another upload.

     Applies the exact archive retained by a company project scan in the same organization. A plan
    reviewed for a personal project or another organization is refused. The normal scanner and apply
    admission gates run again; the reviewed source expires after one hour. Requires the deploy role.

    Args:
        organization (str):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return sync_detailed(
        organization=organization,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
) -> Response[ApplyResponse | Problem]:
    """Deploy a company project's reviewed source without another upload.

     Applies the exact archive retained by a company project scan in the same organization. A plan
    reviewed for a personal project or another organization is refused. The normal scanner and apply
    admission gates run again; the reviewed source expires after one hour. Requires the deploy role.

    Args:
        organization (str):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyCachedProjectPlanRequest,
) -> ApplyResponse | Problem | None:
    """Deploy a company project's reviewed source without another upload.

     Applies the exact archive retained by a company project scan in the same organization. A plan
    reviewed for a personal project or another organization is refused. The normal scanner and apply
    admission gates run again; the reviewed source expires after one hour. Requires the deploy role.

    Args:
        organization (str):
        body (ApplyCachedProjectPlanRequest): Applies an account-owned reviewed project archive
            with selected workloads and optional initial sealed configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            client=client,
            body=body,
        )
    ).parsed
