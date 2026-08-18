# Token Zen Lite 后端镜像（tzl 二进制）。
# 构建上下文为仓库根目录：docker build -t tokenzen/tzl .

FROM golang:1.25-alpine AS build
WORKDIR /src
# 先单独拷依赖清单，依赖未变时复用缓存层
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" -o /out/tzl ./cmd/tzl

FROM alpine:3.21
# ca-certificates：调用上游厂商 HTTPS 接口所需的根证书
# tzdata：时段倍率按 IANA 时区（如 Asia/Shanghai）求值，缺时区库会让规则保存失败
# postgresql17-client、bash、curl：deploy/ 下的备份与恢复脚本所需。缺 pg_dump 时
#   备份脚本无法执行，会造成"以为每天在备份、实际没有任何产物"的状态。
#   客户端大版本高于服务端可正常工作，pg_dump 要求的是客户端不低于服务端。
RUN apk add --no-cache ca-certificates tzdata wget bash curl postgresql17-client && \
    adduser -D -u 10001 tzl && \
    mkdir -p /backup && chown tzl:tzl /backup
# 备份与恢复脚本随镜像分发：容器内没有宿主机的 deploy/ 目录，
# 不拷进来则 README 中"在后端容器内执行"的运维命令全部不存在。
COPY deploy/backup.sh deploy/backup-secrets.sh deploy/restore.sh /opt/tzl/deploy/
COPY --from=build /out/tzl /usr/local/bin/tzl
# 脚本默认在 /opt/tzl 下找 tzl 二进制以复用告警通道；容器内二进制在 /usr/local/bin。
ENV TZL_BIN=/usr/local/bin/tzl BACKUP_DIR=/backup
USER tzl
EXPOSE 19030
# 容器内必须监听全部网卡，否则同网络的 web 容器连不上
ENV TZL_BIND_ADDR=0.0.0.0 TZL_PORT=19030
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:19030/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tzl"]
CMD ["serve"]
