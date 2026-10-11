from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.work_policy_list_response import WorkPolicyListResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/work-policies".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkPolicyListResponse | None:
    if response.status_code == 200:
        response_200 = WorkPolicyListResponse.from_dict(response.json())

        return response_200

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
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkPolicyListResponse]:
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
    environment: str | Unset = UNSET,
) -> Response[Problem | WorkPolicyListResponse]:
    """List named work policies for an app.

     Stage reads return the complete desired policy collection from immutable workload settings. An
    uninitialized stage collection returns 409 and never inherits production policies. Stage policy
    execution remains unavailable until work lanes and producers are isolated.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkPolicyListResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Problem | WorkPolicyListResponse | None:
    """List named work policies for an app.

     Stage reads return the complete desired policy collection from immutable workload settings. An
    uninitialized stage collection returns 409 and never inherits production policies. Stage policy
    execution remains unavailable until work lanes and producers are isolated.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkPolicyListResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Response[Problem | WorkPolicyListResponse]:
    """List named work policies for an app.

     Stage reads return the complete desired policy collection from immutable workload settings. An
    uninitialized stage collection returns 409 and never inherits production policies. Stage policy
    execution remains unavailable until work lanes and producers are isolated.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkPolicyListResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Problem | WorkPolicyListResponse | None:
    """List named work policies for an app.

     Stage reads return the complete desired policy collection from immutable workload settings. An
    uninitialized stage collection returns 409 and never inherits production policies. Stage policy
    execution remains unavailable until work lanes and producers are isolated.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkPolicyListResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            environment=environment,
        )
    ).parsed
