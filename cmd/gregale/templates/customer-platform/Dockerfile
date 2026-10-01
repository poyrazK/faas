FROM node:22-bookworm-slim
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
# Owner tools and their credential journals never belong in the runtime image.
COPY app/ ./app/
ENV PORT=8080
EXPOSE 8080
USER node
CMD ["node", "app/server.js"]
