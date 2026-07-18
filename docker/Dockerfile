FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app

COPY iptv-server /app/iptv-server

RUN chmod +x /app/iptv-server

EXPOSE 8023

ENTRYPOINT ["/app/iptv-server"]
