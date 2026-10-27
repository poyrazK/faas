from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_subject_signal_request import OperationSubjectSignalRequest
from ...models.operation_subject_signal_response import OperationSubjectSignalResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: OperationSubjectSignalRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/operation-signals".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationSubjectSignalResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationSubjectSignalResponse.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[OperationSubjectSignalResponse | Problem]:
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
    body: OperationSubjectSignalRequest,
) -> Response[OperationSubjectSignalResponse | Problem]:
    """Deliver a named event to every active operation about one business subject.

     Requires deploy:write and MFA. Reaches at most 16 active operations of the named definition in the
    scope whose subject matches, oldest first; more is a limit error. Operations that settle during
    delivery are skipped. Each delivery is idempotent per operation by signal_id.

    Args:
        slug (str):
        body (OperationSubjectSignalRequest): One named event for every active operation of a
            definition about one business subject.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationSubjectSignalResponse | Problem]
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
    body: OperationSubjectSignalRequest,
) -> OperationSubjectSignalResponse | Problem | None:
    """Deliver a named event to every active operation about one business subject.

     Requires deploy:write and MFA. Reaches at most 16 active operations of the named definition in the
    scope whose subject matches, oldest first; more is a limit error. Operations that settle during
    delivery are skipped. Each delivery is idempotent per operation by signal_id.

    Args:
        slug (str):
        body (OperationSubjectSignalRequest): One named event for every active operation of a
            definition about one business subject.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationSubjectSignalResponse | Problem
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
    body: OperationSubjectSignalRequest,
) -> Response[OperationSubjectSignalResponse | Problem]:
    """Deliver a named event to every active operation about one business subject.

     Requires deploy:write and MFA. Reaches at most 16 active operations of the named definition in the
    scope whose subject matches, oldest first; more is a limit error. Operations that settle during
    delivery are skipped. Each delivery is idempotent per operation by signal_id.

    Args:
        slug (str):
        body (OperationSubjectSignalRequest): One named event for every active operation of a
            definition about one business subject.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationSubjectSignalResponse | Problem]
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
    body: OperationSubjectSignalRequest,
) -> OperationSubjectSignalResponse | Problem | None:
    """Deliver a named event to every active operation about one business subject.

     Requires deploy:write and MFA. Reaches at most 16 active operations of the named definition in the
    scope whose subject matches, oldest first; more is a limit error. Operations that settle during
    delivery are skipped. Each delivery is idempotent per operation by signal_id.

    Args:
        slug (str):
        body (OperationSubjectSignalRequest): One named event for every active operation of a
            definition about one business subject.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationSubjectSignalResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
