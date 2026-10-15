# syntax=docker/dockerfile:1
FROM node:24-bookworm-slim
WORKDIR /app
COPY --chown=node:node package.json package-lock.json ./
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/proxy_ca; fi; \
    npm ci --omit=dev --ignore-scripts --strict-ssl=true --no-audit --no-fund
COPY --chown=node:node server.mjs auth.mjs audit.mjs audit-postgres.mjs queries.mjs timeline.mjs customer.mjs actions.mjs support-console.json ./
COPY --chown=node:node public ./public
USER node
EXPOSE 8080
CMD ["node","server.mjs"]
