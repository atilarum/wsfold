FROM golang:1.25-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends git sudo util-linux ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY scripts/native-bind-e2e.sh /usr/local/bin/native-bind-e2e.sh
COPY dist/wsfold-native-bind-e2e /usr/local/bin/wsfold
RUN chmod +x /usr/local/bin/native-bind-e2e.sh /usr/local/bin/wsfold

ENTRYPOINT ["/usr/local/bin/native-bind-e2e.sh", "container"]
