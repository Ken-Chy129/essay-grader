# 作文批改助手(语文)

给高中语文老师用的作文批改辅助工具:上传学生**手写作文照片**,系统自动**方向校正 →
转写 → 分维度评分 + 评语**,并在整批批改后产出**数据洞察**。设计共识见 [CONTEXT.md](./CONTEXT.md)。

## 核心流程

1. **新建作业**:填(或从照片识别)作文题目,设满分与评分维度(默认 立意/结构/语言/内容 各 15)。
2. **上传作文**:一篇作文 = 该生的 1~N 张页面照片(支持跨页);照片方向自动校正。
3. **异步批改**:后台两阶段跑(图→转写稿→评语/评分),逐篇完成逐篇可见。
4. **审核**:可编辑转写稿后“重新批改”;评分/评语均为 AI 草稿,老师定稿。
5. **数据洞察**:整批跑完后看分数分布、维度短板、共性问题、讲评选篇。

## 运行

```bash
ANTHROPIC_BASE_URL=https://your-anthropic-gateway.example.com \
ANTHROPIC_API_KEY=sk-xxx \
MODEL=claude-opus-4-8 \
go run .
```

打开 http://localhost:8787(`MODEL` 可省,默认 `claude-opus-4-8`)。

## Docker

```bash
# 构建
docker build -t essay-grader:latest .

# 运行(网关与密钥运行时注入,不在镜像里;data 用卷持久化)
docker run -d --name essay-grader \
  -p 8787:8787 \
  -e ANTHROPIC_BASE_URL=https://your-anthropic-gateway.example.com \
  -e ANTHROPIC_API_KEY=sk-xxx \
  -v "$PWD/data:/app/data" \
  essay-grader:latest
```

打开 http://localhost:8787。`MODEL` 镜像内置默认 `claude-opus-4-8`,可用 `-e` 覆盖;
`ANTHROPIC_BASE_URL` / `ANTHROPIC_API_KEY` 必须运行时注入。多阶段构建,运行镜像基于 alpine
(含 CA 证书以访问网关 HTTPS),静态二进制、无 cgo。

## 技术

- 纯 Go 标准库,**零外部依赖**;JSON 文件持久化(`data/`),原图存 `data/uploads/`。
- 模型走 Anthropic 兼容网关(`/v1/messages`),Opus 4.8。架构见 [docs/adr/0001](./docs/adr/0001-two-stage-pipeline.md)。
- 前端:后端直接 serve 的单页 vanilla SPA(`web/`),无构建步骤。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ANTHROPIC_BASE_URL` | (必填) | Anthropic 兼容网关地址 |
| `ANTHROPIC_API_KEY` | (必填) | 网关密钥 |
| `MODEL` | claude-opus-4-8 | 模型名 |
| `PORT` | 8787 | 监听端口 |
| `DATA_DIR` | data | 数据目录 |
