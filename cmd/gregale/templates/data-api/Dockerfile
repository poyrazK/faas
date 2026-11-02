FROM postgrest/postgrest@sha256:7c5840727ad683b0fdbfb9810930665b34f8b44884a4dc740aa8844748a2b85e AS postgrest
# Pinned by digest like the PostgREST stage: the platform builds this runtime
# for every Data API, so its base must not drift between builds.
FROM node:22-trixie-slim@sha256:2117f713a417f3aff682ac8f5bc2ebbdf1beed51f36e67ab1e0fd3866b6d2d8f
WORKDIR /app
COPY --from=postgrest /bin/postgrest /usr/local/bin/postgrest
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY config.mjs access.mjs server.mjs types.mjs type-render.mjs logging.mjs timing.mjs database-timing.mjs refresh.mjs refresh-rotation.mjs admission.mjs health.mjs pool.mjs deadline.mjs sql-deadline.mjs response-limit.mjs rate-limit.mjs user-concurrency.mjs connection-observations.mjs usage.mjs storage.mjs engine.mjs changes.mjs runtime-version.json ./
EXPOSE 8080
CMD ["node", "server.mjs"]
