FROM node:22-bookworm-slim

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts

COPY handler.js secret-reload.js ./
ENV PORT=8080
EXPOSE 8080
LABEL com.gregale.secret-reload-signal="SIGHUP"
USER node
CMD ["node", "handler.js"]
