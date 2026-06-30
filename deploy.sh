#!/usr/bin/env bash
# 一键发布:拉代码 → 构建镜像 → 重启容器。
# 密钥等环境变量放在同目录、不入 git 的 .env.local(至少需含 ANTHROPIC_API_KEY)。
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -f .env.local ]; then
  echo "缺少 .env.local(应包含 ANTHROPIC_API_KEY=sk-...)" >&2
  exit 1
fi

echo "==> git pull"
git pull --ff-only

echo "==> docker build"
docker build -t essay-grader:latest .

echo "==> restart container"
docker rm -f essay-grader >/dev/null 2>&1 || true
docker run -d --name essay-grader --restart unless-stopped \
  -p 127.0.0.1:8787:8787 \
  --env-file .env.local \
  -v "$PWD/data:/app/data" \
  essay-grader:latest >/dev/null

sleep 2
echo "==> status"
docker ps --filter name=essay-grader --format "{{.Names}} {{.Status}} {{.Ports}}"
curl -s -o /dev/null -w "本机自测 127.0.0.1:8787 -> %{http_code}\n" http://127.0.0.1:8787/ || true
echo "完成。"
