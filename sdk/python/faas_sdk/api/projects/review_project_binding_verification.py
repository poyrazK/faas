from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.project_binding_verification_review import ProjectBindingVerificationReview
from ...models.review_project_binding_verification_request import ReviewProjectBindingVerificationRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: ReviewProjectBindingVerificationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/review".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ProjectBindingVerificationReview | None:
    if response.status_code == 200:
        response_200 = ProjectBindingVerificationReview.from_dict(response.json())

        return response_200

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ProjectBindingVerificationReview]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReviewProjectBindingVerificationRequest,
) -> Response[Any | ProjectBindingVerificationReview]:
    """Review verification without creating receipts or admitting work

    Args:
        slug (str):
        environment (str):
        body (ReviewProjectBindingVerificationRequest): Project release selection to review
            without creating receipts or starting verification.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationReview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReviewProjectBindingVerificationRequest,
) -> Any | ProjectBindingVerificationReview | None:
    """Review verification without creating receipts or admitting work

    Args:
        slug (str):
        environment (str):
        body (ReviewProjectBindingVerificationRequest): Project release selection to review
            without creating receipts or starting verification.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationReview
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReviewProjectBindingVerificationRequest,
) -> Response[Any | ProjectBindingVerificationReview]:
    """Review verification without creating receipts or admitting work

    Args:
        slug (str):
        environment (str):
        body (ReviewProjectBindingVerificationRequest): Project release selection to review
            without creating receipts or starting verification.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationReview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReviewProjectBindingVerificationRequest,
) -> Any | ProjectBindingVerificationReview | None:
    """Review verification without creating receipts or admitting work

    Args:
        slug (str):
        environment (str):
        body (ReviewProjectBindingVerificationRequest): Project release selection to review
            without creating receipts or starting verification.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationReview
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
