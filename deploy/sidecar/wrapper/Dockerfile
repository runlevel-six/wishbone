# Extraction sidecar: a thin HTTP shim over get-product-name (MIT), recorded in
# the repository NOTICE file.
FROM node:22-alpine

WORKDIR /app

COPY package.json ./
RUN npm install --omit=dev --no-audit --no-fund

COPY server.js ./

ENV HOST=127.0.0.1
ENV PORT=8081
EXPOSE 8081
USER node

CMD ["node", "server.js"]
