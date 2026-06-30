package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := env("MODEL", "claude-opus-4-8")
	port := env("PORT", "8787")
	dataDir := env("DATA_DIR", "data")
	webDir := env("WEB_DIR", "web")
	appPassword := os.Getenv("APP_PASSWORD") // 留空=不鉴权

	if apiKey == "" || baseURL == "" {
		log.Fatal("缺少 ANTHROPIC_API_KEY 或 ANTHROPIC_BASE_URL 环境变量")
	}

	abs, _ := filepath.Abs(dataDir)
	store, err := NewStore(dataDir)
	if err != nil {
		log.Fatalf("初始化存储失败: %v", err)
	}
	llm := NewLLM(baseURL, apiKey, model)
	pipe := NewPipeline(store, llm, 2)
	srv := NewServer(store, llm, pipe, webDir, appPassword)

	authMsg := "无鉴权"
	if appPassword != "" {
		authMsg = "已启用登录口令"
	}
	log.Printf("作文批改助手启动:http://localhost:%s  (模型=%s, 网关=%s, 数据=%s, %s)", port, model, baseURL, abs, authMsg)
	if err := http.ListenAndServe(":"+port, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
