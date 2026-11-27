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
 && xz -dc /tmp/7z.tar.xz | tar -x -C /tmp/7z \
 && cp /tmp/7z/7zzs /7zzs \
 && /7zzs i | grep -q -i rar

# par2cmdline-turbo's static build, for repairing usenet downloads from disk
# (the pure-Go repair holds the whole release in memory).
FROM alpine:3.20 AS par2
ARG PAR2_VERSION=1.5.0
ARG PAR2_SHA256=5a9f64386813456693c2ea1fb7649436fe7544bbdf97fd73b3483dfcc8aca464
RUN apk add --no-cache curl unzip \
 && curl -fsSL -o /tmp/par2.zip https://github.com/animetosho/par2cmdline-turbo/releases/download/v${PAR2_VERSION}/par2cmdline-turbo-${PAR2_VERSION}-linux-amd64.zip \
 && echo "${PAR2_SHA256}  /tmp/par2.zip" | sha256sum -c - \
 && unzip -o /tmp/par2.zip -d /tmp/p && cp /tmp/p/par2 /par2 && chmod 755 /par2 \
 && /par2 -V

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/undermynd /undermynd
COPY --from=sevenzip /7zzs /7zzs
COPY --from=par2 /par2 /par2
ENV UNDERMYND_CONFIG=/config UNDERMYND_DATA=/data PUID=99 PGID=100
EXPOSE 8080 8081
VOLUME ["/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/undermynd", "healthcheck"]
ENTRYPOINT ["/undermynd"]
