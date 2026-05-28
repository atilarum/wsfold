FROM golang:1.25-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends git sudo util-linux ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace/wsfold
COPY . .
RUN chmod +x /workspace/wsfold/dist/wsfold-native-bind-e2e

ENV WSFOLD_NATIVE_BIND_E2E=1
ENV WSFOLD_E2E_WSFOLD_BINARY=/workspace/wsfold/dist/wsfold-native-bind-e2e

ENTRYPOINT ["go", "test", "./internal/e2e/nativebind", "-count=1", "-v"]
