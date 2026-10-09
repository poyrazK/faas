FROM postgrest/postgrest@sha256:7c5840727ad683b0fdbfb9810930665b34f8b44884a4dc740aa8844748a2b85e AS postgrest
FROM node:22-bookworm-slim
WORKDIR /app
COPY --from=postgrest /bin/postgrest /usr/local/bin/postgrest
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY config.mjs server.mjs types.mjs logging.mjs ./
EXPOSE 8080
CMD ["node", "server.mjs"]
