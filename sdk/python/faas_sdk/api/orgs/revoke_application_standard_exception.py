from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_exception import ApplicationStandardException
from ...models.problem import Problem
from ...models.revoke_application_standard_exception_request import RevokeApplicationStandardExceptionRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    app: UUID,
    exception: UUID,
    *,
    body: RevokeApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/application-standard-enrollments/{app}/exceptions/{exception}/revoke".format(
            slug=quote(str(slug), safe=""),
            app=quote(str(app), safe=""),
            exception=quote(str(exception), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardException | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardException.from_dict(response.json())

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
    exception: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RevokeApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardException | Problem]:
    """Revoke an application exception and retain its approval history

     Requires an active owner or admin and the current desired revision. The original approval, reason
    and value remain immutable; revocation queues reevaluation under the ordinary inherited controls. A
    conflicting local setting can leave the application blocked until it is corrected.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        exception (UUID):
        idempotency_key (str | Unset):
        body (RevokeApplicationStandardExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardException | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
        exception=exception,
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
    exception: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RevokeApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardException | Problem | None:
    """Revoke an application exception and retain its approval history

     Requires an active owner or admin and the current desired revision. The original approval, reason
    and value remain immutable; revocation queues reevaluation under the ordinary inherited controls. A
    conflicting local setting can leave the application blocked until it is corrected.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        exception (UUID):
        idempotency_key (str | Unset):
        body (RevokeApplicationStandardExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardException | Problem
    """

    return sync_detailed(
        slug=slug,
        app=app,
        exception=exception,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    app: UUID,
    exception: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RevokeApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardException | Problem]:
    """Revoke an application exception and retain its approval history

     Requires an active owner or admin and the current desired revision. The original approval, reason
    and value remain immutable; revocation queues reevaluation under the ordinary inherited controls. A
    conflicting local setting can leave the application blocked until it is corrected.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        exception (UUID):
        idempotency_key (str | Unset):
        body (RevokeApplicationStandardExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardException | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
        exception=exception,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    app: UUID,
    exception: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RevokeApplicationStandardExceptionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardException | Problem | None:
    """Revoke an application exception and retain its approval history

     Requires an active owner or admin and the current desired revision. The original approval, reason
    and value remain immutable; revocation queues reevaluation under the ordinary inherited controls. A
    conflicting local setting can leave the application blocked until it is corrected.
    Disabled by default until the application standards release acceptance gates pass.
    Idempotency replay requires the current role and release gate. Saved intent is not consumer
    observation.

    Args:
        slug (str):
        app (UUID):
        exception (UUID):
        idempotency_key (str | Unset):
        body (RevokeApplicationStandardExceptionRequest):

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
            exception=exception,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
