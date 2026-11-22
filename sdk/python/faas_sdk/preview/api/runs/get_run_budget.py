from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.run_budget_kind import RunBudgetKind
from ...models.run_budget_policy import RunBudgetPolicy
from ...types import Response


def _get_kwargs(
    kind: RunBudgetKind,
    ref: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/run-controls/budgets/{kind}/{ref}".format(
            kind=quote(str(kind), safe=""),
            ref=quote(str(ref), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RunBudgetPolicy | None:
    if response.status_code == 200:
        response_200 = RunBudgetPolicy.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RunBudgetPolicy]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    kind: RunBudgetKind,
    ref: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | RunBudgetPolicy]:
    """Read an account-owned customer or task budget and current UTC-day usage.

    Args:
        kind (RunBudgetKind): Whether a budget applies to one customer or a named task.
        ref (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RunBudgetPolicy]
    """

    kwargs = _get_kwargs(
        kind=kind,
        ref=ref,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    kind: RunBudgetKind,
    ref: str,
    *,
    client: AuthenticatedClient,
) -> Problem | RunBudgetPolicy | None:
    """Read an account-owned customer or task budget and current UTC-day usage.

    Args:
        kind (RunBudgetKind): Whether a budget applies to one customer or a named task.
        ref (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RunBudgetPolicy
    """

    return sync_detailed(
        kind=kind,
        ref=ref,
        client=client,
    ).parsed


async def asyncio_detailed(
    kind: RunBudgetKind,
    ref: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | RunBudgetPolicy]:
    """Read an account-owned customer or task budget and current UTC-day usage.

    Args:
        kind (RunBudgetKind): Whether a budget applies to one customer or a named task.
        ref (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RunBudgetPolicy]
    """

    kwargs = _get_kwargs(
        kind=kind,
        ref=ref,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    kind: RunBudgetKind,
    ref: str,
    *,
    client: AuthenticatedClient,
) -> Problem | RunBudgetPolicy | None:
    """Read an account-owned customer or task budget and current UTC-day usage.

    Args:
        kind (RunBudgetKind): Whether a budget applies to one customer or a named task.
        ref (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RunBudgetPolicy
    """

    return (
        await asyncio_detailed(
            kind=kind,
            ref=ref,
            client=client,
        )
    ).parsed
