# Extraction sidecar.
#
# Verify the license of get-product-data before shipping this image and record
# it in the repository NOTICE file (plan §12).
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
