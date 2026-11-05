from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_initial_failures import WorkflowInitialFailures
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    workflow: str | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["workflow"] = workflow

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-failures".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowInitialFailures | None:
    if response.status_code == 200:
        response_200 = WorkflowInitialFailures.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowInitialFailures]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[Problem | WorkflowInitialFailures]:
    """List captured initial workflow failures

     Read-only, policy-independent generation-zero failure inbox. Capture is prospective and partial,
    retained for 30 days with at most 10000 entries per app. Newest-first cursors bind the account, app,
    workflow filter and observation ceiling. Retention may remove entries between pages. Evidence alone
    does not prove that a run can safely resume. No run inputs, outputs or error text are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialFailures]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow=workflow,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Problem | WorkflowInitialFailures | None:
    """List captured initial workflow failures

     Read-only, policy-independent generation-zero failure inbox. Capture is prospective and partial,
    retained for 30 days with at most 10000 entries per app. Newest-first cursors bind the account, app,
    workflow filter and observation ceiling. Retention may remove entries between pages. Evidence alone
    does not prove that a run can safely resume. No run inputs, outputs or error text are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialFailures
    """

    return sync_detailed(
        slug=slug,
        client=client,
        workflow=workflow,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[Problem | WorkflowInitialFailures]:
    """List captured initial workflow failures

     Read-only, policy-independent generation-zero failure inbox. Capture is prospective and partial,
    retained for 30 days with at most 10000 entries per app. Newest-first cursors bind the account, app,
    workflow filter and observation ceiling. Retention may remove entries between pages. Evidence alone
    does not prove that a run can safely resume. No run inputs, outputs or error text are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialFailures]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow=workflow,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Problem | WorkflowInitialFailures | None:
    """List captured initial workflow failures

     Read-only, policy-independent generation-zero failure inbox. Capture is prospective and partial,
    retained for 30 days with at most 10000 entries per app. Newest-first cursors bind the account, app,
    workflow filter and observation ceiling. Retention may remove entries between pages. Evidence alone
    does not prove that a run can safely resume. No run inputs, outputs or error text are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialFailures
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            workflow=workflow,
            after=after,
            limit=limit,
        )
    ).parsed
