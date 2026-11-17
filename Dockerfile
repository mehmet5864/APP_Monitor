# --- Derleme ---
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/appmon-server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/appmon-agent ./cmd/agent \
 && mkdir -p /out/agents \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/agents/appmon-agent-linux-amd64 ./cmd/agent \
 && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/agents/appmon-agent-linux-arm64 ./cmd/agent \
 && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/agents/appmon-agent-windows-amd64.exe ./cmd/agent \
 && CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/agents/appmon-agent-windows-arm64.exe ./cmd/agent

# --- Agent ikili dosyasını dışarı almak için: docker build --target agent-bin --output ./dist .
FROM scratch AS agent-bin
COPY --from=build /out/appmon-agent /appmon-agent

# --- Sunucu imajı ---
FROM alpine:3.20
# chromium: tarayıcı kaydı ve tarayıcı senaryolarının oynatılması için (imajı ~300-400 MB büyütür)
RUN apk add --no-cache ca-certificates tzdata chromium ttf-dejavu font-liberation libcap openssh-client && adduser -D -u 10001 appmon && mkdir -p /data && chown appmon /data
ENV APM_CHROME_PATH=/usr/bin/chromium APM_CHROME_NO_SANDBOX=true APM_DATA_DIR=/data
COPY --from=build /out/appmon-server /usr/local/bin/appmon-server
# traceroute (ham ICMP) icin kok olmadan CAP_NET_RAW
RUN setcap cap_net_raw+ep /usr/local/bin/appmon-server
COPY --from=build /out/agents /usr/share/appmon/agents
ENV APM_AGENT_DIR=/usr/share/appmon/agents
USER appmon
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/appmon-server"]
