# ── 构建阶段：静态二进制（纯 Go 驱动，CGO 关闭）──
FROM golang:1.27-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/mockc2 .

# ── 运行阶段：最小镜像 ──
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 mockc2
WORKDIR /opt/mockc2
COPY --from=build /out/mockc2 /opt/mockc2/mockc2
COPY www /opt/mockc2/www
COPY mockc2_settings.example.json /opt/mockc2/mockc2_settings.example.json
RUN mkdir -p /opt/mockc2/capture /opt/mockc2/certs /opt/mockc2/data \
    && chown -R mockc2:mockc2 /opt/mockc2
USER mockc2
ENV MOCKC2_ROOT=/opt/mockc2 TZ=Asia/Shanghai
EXPOSE 9100 9443
ENTRYPOINT ["/opt/mockc2/mockc2"]
CMD ["--no-https"]
