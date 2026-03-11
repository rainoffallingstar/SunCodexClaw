FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS go-build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/suncodexclawd ./cmd/suncodexclawd

FROM node:20-bookworm-slim

WORKDIR /app

ENV NODE_ENV=production
ENV CODEX_HOME=/home/node/.codex

RUN apt-get update \
  && apt-get install -y --no-install-recommends bash ca-certificates procps tini \
  && rm -rf /var/lib/apt/lists/*

# Install Codex CLI (package name can be overridden at build time)
ARG CODEX_NPM_PKG=@openai/codex
RUN npm install -g "${CODEX_NPM_PKG}" \
  && command -v codex >/dev/null 2>&1

# Install only production deps first (better layer caching)
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

# Copy the rest
COPY . .

COPY --from=go-build /out/suncodexclawd /app/bin/suncodexclawd
RUN chmod +x /app/bin/suncodexclawd

RUN chmod +x /app/tools/docker_entrypoint.sh

USER node

VOLUME ["/home/node/.codex", "/app/config", "/app/.runtime"]

EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini","--","/app/tools/docker_entrypoint.sh"]
CMD ["start"]
