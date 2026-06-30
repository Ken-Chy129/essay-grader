# ---- 构建阶段 ----
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
# 纯标准库、无 cgo,编译为静态二进制
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/essaygrader .

# ---- 运行阶段 ----
FROM alpine:3.20
# ca-certificates:访问网关 HTTPS 所需
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/essaygrader /app/essaygrader
COPY web /app/web
RUN mkdir -p /app/data && chown -R app /app/data
USER app
ENV WEB_DIR=/app/web \
    DATA_DIR=/app/data \
    PORT=8787 \
    MODEL=claude-opus-4-8
# ANTHROPIC_BASE_URL 与 ANTHROPIC_API_KEY 运行时注入(见 .env.local / docker run -e)
EXPOSE 8787
VOLUME ["/app/data"]
ENTRYPOINT ["/app/essaygrader"]
