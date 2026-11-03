# --- Derleme ---
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/appmon-server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/appmon-agent ./cmd/agent

# --- Agent ikili dosyasını dışarı almak için: docker build --target agent-bin --output ./dist .
FROM scratch AS agent-bin
COPY --from=build /out/appmon-agent /appmon-agent

# --- Sunucu imajı ---
FROM alpine:3.20
# chromium: tarayıcı kaydı ve tarayıcı senaryolarının oynatılması için (imajı ~300-400 MB büyütür)
RUN apk add --no-cache ca-certificates tzdata chromium ttf-dejavu font-liberation && adduser -D -u 10001 appmon
ENV APM_CHROME_PATH=/usr/bin/chromium APM_CHROME_NO_SANDBOX=true
COPY --from=build /out/appmon-server /usr/local/bin/appmon-server
USER appmon
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/appmon-server"]
