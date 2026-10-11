# CC_DashBoard Master 多阶段构建
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
# COPY go.sum ./  # 依赖锁定后取消注释
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/master ./cmd/master

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/master /app/master
VOLUME /app/data
EXPOSE 8080
ENTRYPOINT ["/app/master"]
