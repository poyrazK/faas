from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_exception import ApplicationStandardException
from ...models.approve_application_standard_exception_request import ApproveApplicationStandardExceptionRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    app: UUID,
    *,
    body: ApproveApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/application-standard-enrollments/{app}/exceptions".format(
            slug=quote(str(slug), safe=""),
            app=quote(str(app), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardException | Problem | None:
    if response.status_code == 201:
        response_201 = ApplicationStandardException.from_dict(response.json())

        return response_201

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
) -> Response[ApplicationStandardException | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardException | Problem]:
    """Approve a bounded application exception

     Requires an active owner or admin. Approves one field for an adopted immutable standard version; all
    independent constraints and platform controls still apply. The server enforces a future expiry
    within 30 days and queues a new desired revision.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        idempotency_key (str | Unset):
        body (ApproveApplicationStandardExceptionRequest): Time-limited, reasoned exception for
            one adopted standard field, bound to the enrollment revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardException | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardException | Problem | None:
    """Approve a bounded application exception

     Requires an active owner or admin. Approves one field for an adopted immutable standard version; all
    independent constraints and platform controls still apply. The server enforces a future expiry
    within 30 days and queues a new desired revision.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        idempotency_key (str | Unset):
        body (ApproveApplicationStandardExceptionRequest): Time-limited, reasoned exception for
            one adopted standard field, bound to the enrollment revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardException | Problem
    """

    return sync_detailed(
        slug=slug,
        app=app,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardException | Problem]:
    """Approve a bounded application exception

     Requires an active owner or admin. Approves one field for an adopted immutable standard version; all
    independent constraints and platform controls still apply. The server enforces a future expiry
    within 30 days and queues a new desired revision.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        idempotency_key (str | Unset):
        body (ApproveApplicationStandardExceptionRequest): Time-limited, reasoned exception for
            one adopted standard field, bound to the enrollment revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardException | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardException | Problem | None:
    """Approve a bounded application exception

     Requires an active owner or admin. Approves one field for an adopted immutable standard version; all
    independent constraints and platform controls still apply. The server enforces a future expiry
    within 30 days and queues a new desired revision.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        idempotency_key (str | Unset):
        body (ApproveApplicationStandardExceptionRequest): Time-limited, reasoned exception for
            one adopted standard field, bound to the enrollment revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardException | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            app=app,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
