FROM node:22-bookworm-slim
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY server.mjs ./
COPY migrations ./migrations
EXPOSE 8080
USER node
CMD ["node", "server.mjs"]
