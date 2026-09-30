FROM node:22-bookworm-slim AS sdk
WORKDIR /sdk
COPY sdk/node/package.json sdk/node/package-lock.json ./
RUN npm ci --ignore-scripts
COPY sdk/node/tsconfig.json ./
COPY sdk/node/src/ ./src/
RUN npm run build

FROM node:22-bookworm-slim
WORKDIR /workspace
COPY --from=sdk /sdk/dist/ ./sdk/node/dist/
COPY sdk/node/package.json ./sdk/node/package.json
COPY tests/dev-bridge-acceptance/app/server.mjs ./tests/dev-bridge-acceptance/app/server.mjs
ENV PORT=8080
EXPOSE 8080
USER node
# The shared image can answer characterization probes before its per-workload
# Compose command selects frontend, payments or inventory.
CMD ["node", "tests/dev-bridge-acceptance/app/server.mjs", "inventory"]
