FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS frontend
WORKDIR /web
COPY frontend/package*.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS wordaddin
WORKDIR /addin
COPY word-addin/package*.json ./
RUN npm ci --ignore-scripts
COPY word-addin/ ./
RUN npm run build -- --base /word-addin/

FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine AS server
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=frontend /web/dist/ ./web/assets/
COPY --from=wordaddin /addin/dist/ ./web/assets/word-addin/
COPY word-addin/manifest.xml ./web/assets/word-addin/manifest.template.xml
COPY NOTICE.md ./web/assets/third-party-notices.txt
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION -X main.revision=$REVISION" -o /casecite ./cmd/casecite
RUN mkdir /data /provisioning && chown 65532:65532 /data /provisioning && chmod 700 /data /provisioning

FROM alpine:3.22
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="CaseCite by PairSys" \
      org.opencontainers.image.source="https://github.com/PAIR-Systems-Inc/casecite-goodmem" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION \
      ai.pairsys.casecite.doctor="1"
COPY --from=server /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=server --chown=65532:65532 /data /data
COPY --from=server --chown=65532:65532 /provisioning /provisioning
COPY --from=server /casecite /usr/local/bin/casecite
USER 65532:65532
WORKDIR /data
EXPOSE 8000
ENTRYPOINT ["casecite"]
