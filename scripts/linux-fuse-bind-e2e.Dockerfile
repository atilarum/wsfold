FROM golang:1.25-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends bindfs fuse3 git util-linux ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace/wsfold
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/wsfold ./cmd/wsfold \
    && useradd --create-home --shell /bin/bash wsfold \
    && chown -R wsfold:wsfold /workspace/wsfold /go

ENV WSFOLD_LINUX_FUSE_BIND_E2E=1
ENV WSFOLD_E2E_WSFOLD_BINARY=/usr/local/bin/wsfold
ENV GOCACHE=/tmp/wsfold-go-build

USER wsfold

ENTRYPOINT ["go", "test", "./internal/e2e/fusebind", "-count=1", "-v"]
