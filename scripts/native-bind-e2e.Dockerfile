FROM golang:1.25-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends git sudo util-linux ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace/wsfold
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/wsfold ./cmd/wsfold

ENV WSFOLD_NATIVE_BIND_E2E=1
ENV WSFOLD_E2E_WSFOLD_BINARY=/usr/local/bin/wsfold

ENTRYPOINT ["go", "test", "./internal/e2e/nativebind", "-count=1", "-v"]
