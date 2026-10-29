FROM node:24-bookworm-slim AS frontend
WORKDIR /web
COPY frontend/package*.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

FROM node:24-bookworm-slim AS wordaddin
WORKDIR /addin
COPY word-addin/package*.json ./
RUN npm ci --ignore-scripts
COPY word-addin/ ./
RUN npm run build -- --base /word-addin/

FROM golang:1.26.10-alpine AS server
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=frontend /web/dist/ ./web/assets/
COPY --from=wordaddin /addin/dist/ ./web/assets/word-addin/
COPY word-addin/manifest.xml ./web/assets/word-addin/manifest.template.xml
COPY NOTICE.md ./web/assets/third-party-notices.txt
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /casecite ./cmd/casecite

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && mkdir /data && chown 65532:65532 /data
COPY --from=server /casecite /usr/local/bin/casecite
USER 65532:65532
WORKDIR /data
EXPOSE 8000
ENTRYPOINT ["casecite"]
