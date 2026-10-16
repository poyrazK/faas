from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.binding_verification_review import BindingVerificationReview
from ...models.review_binding_verification_request import ReviewBindingVerificationRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: ReviewBindingVerificationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/bindings/verification-runs/review".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | BindingVerificationReview | None:
    if response.status_code == 200:
        response_200 = BindingVerificationReview.from_dict(response.json())

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
) -> Response[Any | BindingVerificationReview]:
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
    body: ReviewBindingVerificationRequest,
) -> Response[Any | BindingVerificationReview]:
    """Review app binding verification selection and consent without admitting work

    Args:
        slug (str):
        body (ReviewBindingVerificationRequest): Application deployment and binding selection to
            review without admitting work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | BindingVerificationReview]
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
    body: ReviewBindingVerificationRequest,
) -> Any | BindingVerificationReview | None:
    """Review app binding verification selection and consent without admitting work

    Args:
        slug (str):
        body (ReviewBindingVerificationRequest): Application deployment and binding selection to
            review without admitting work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | BindingVerificationReview
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
    body: ReviewBindingVerificationRequest,
) -> Response[Any | BindingVerificationReview]:
    """Review app binding verification selection and consent without admitting work

    Args:
        slug (str):
        body (ReviewBindingVerificationRequest): Application deployment and binding selection to
            review without admitting work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | BindingVerificationReview]
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
    body: ReviewBindingVerificationRequest,
) -> Any | BindingVerificationReview | None:
    """Review app binding verification selection and consent without admitting work

    Args:
        slug (str):
        body (ReviewBindingVerificationRequest): Application deployment and binding selection to
            review without admitting work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | BindingVerificationReview
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
