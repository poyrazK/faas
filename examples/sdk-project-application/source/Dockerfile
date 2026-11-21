FROM node:22-alpine
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY api.mjs worker.mjs ./
ENV PORT=8080
CMD ["node", "api.mjs"]
