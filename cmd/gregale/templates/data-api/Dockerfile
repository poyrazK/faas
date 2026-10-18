FROM postgrest/postgrest@sha256:7c5840727ad683b0fdbfb9810930665b34f8b44884a4dc740aa8844748a2b85e AS postgrest
FROM node:22-bookworm-slim
WORKDIR /app
COPY --from=postgrest /bin/postgrest /usr/local/bin/postgrest
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY config.mjs access.mjs server.mjs types.mjs type-render.mjs logging.mjs timing.mjs database-timing.mjs refresh.mjs refresh-rotation.mjs admission.mjs health.mjs pool.mjs deadline.mjs sql-deadline.mjs response-limit.mjs rate-limit.mjs user-concurrency.mjs connection-observations.mjs ./
EXPOSE 8080
CMD ["node", "server.mjs"]
