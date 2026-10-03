from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.upsert_work_policy_request import UpsertWorkPolicyRequest
from ...models.work_policy_response import WorkPolicyResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: UpsertWorkPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/work-policies/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkPolicyResponse | None:
    if response.status_code == 200:
        response_200 = WorkPolicyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkPolicyResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpsertWorkPolicyRequest,
) -> Response[Problem | WorkPolicyResponse]:
    """Create or update a named app work policy.

     Policy changes affect new work only. Existing invocations retain their admission settings and policy
    revision.

    Args:
        slug (str):
        name (str):
        body (UpsertWorkPolicyRequest): App work policy settings; durations use whole
            milliseconds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkPolicyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpsertWorkPolicyRequest,
) -> Problem | WorkPolicyResponse | None:
    """Create or update a named app work policy.

     Policy changes affect new work only. Existing invocations retain their admission settings and policy
    revision.

    Args:
        slug (str):
        name (str):
        body (UpsertWorkPolicyRequest): App work policy settings; durations use whole
            milliseconds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkPolicyResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpsertWorkPolicyRequest,
) -> Response[Problem | WorkPolicyResponse]:
    """Create or update a named app work policy.

     Policy changes affect new work only. Existing invocations retain their admission settings and policy
    revision.

    Args:
        slug (str):
        name (str):
        body (UpsertWorkPolicyRequest): App work policy settings; durations use whole
            milliseconds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkPolicyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpsertWorkPolicyRequest,
) -> Problem | WorkPolicyResponse | None:
    """Create or update a named app work policy.

     Policy changes affect new work only. Existing invocations retain their admission settings and policy
    revision.

    Args:
        slug (str):
        name (str):
        body (UpsertWorkPolicyRequest): App work policy settings; durations use whole
            milliseconds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkPolicyResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
