# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/undermynd ./cmd/undermynd

# 7-Zip's official static Linux build, for unpacking usenet downloads: it
# reads every RAR variant (the pure-Go reader stalls on some).
FROM alpine:3.20 AS sevenzip
RUN apk add --no-cache curl xz \
 && mkdir -p /tmp/7z \
 && (curl -fsSL -o /tmp/7z.tar.xz https://github.com/ip7z/7zip/releases/download/25.01/7z2501-linux-x64.tar.xz \
     || curl -fsSL -o /tmp/7z.tar.xz https://github.com/ip7z/7zip/releases/download/24.09/7z2409-linux-x64.tar.xz) \
 && tar -xJf /tmp/7z.tar.xz -C /tmp/7z \
 && cp /tmp/7z/7zzs /7zzs \
 && /7zzs i | grep -q -i rar

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/undermynd /undermynd
COPY --from=sevenzip /7zzs /7zzs
ENV UNDERMYND_CONFIG=/config UNDERMYND_DATA=/data PUID=99 PGID=100
EXPOSE 8080 8081
VOLUME ["/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/undermynd", "healthcheck"]
ENTRYPOINT ["/undermynd"]
