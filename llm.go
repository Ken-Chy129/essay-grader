package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LLM 封装对 Anthropic 兼容网关的调用。
type LLM struct {
	BaseURL string
	APIKey  string
	Model   string
	http    *http.Client
}

func NewLLM(baseURL, apiKey, model string) *LLM {
	return &LLM{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		http:    &http.Client{Timeout: 4 * time.Minute},
	}
}

type contentBlock struct {
	Type   string         `json:"type"`
	Text   string         `json:"text,omitempty"`
	Source *imageSource   `json:"source,omitempty"`
}
type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}
type apiMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}
type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []apiMessage `json:"messages"`
}
type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (l *LLM) call(req apiRequest) (string, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest("POST", l.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("x-api-key", l.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")
	resp, err := l.http.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("网关返回 %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}
	if ar.Error != nil {
		return "", fmt.Errorf("模型错误: %s", ar.Error.Message)
	}
	var sb strings.Builder
	for _, c := range ar.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

func imageBlock(jpegBytes []byte) contentBlock {
	return contentBlock{Type: "image", Source: &imageSource{
		Type: "base64", MediaType: "image/jpeg",
		Data: base64.StdEncoding.EncodeToString(jpegBytes),
	}}
}

var digitsRe = regexp.MustCompile(`\d+`)

// DetectOrientationCW 判定让文字正立需要顺时针旋转的角度(0/90/180/270)。
func (l *LLM) DetectOrientationCW(smallJPEG []byte) (int, error) {
	txt, err := l.call(apiRequest{
		Model: l.Model, MaxTokens: 10,
		Messages: []apiMessage{{Role: "user", Content: []contentBlock{
			imageBlock(smallJPEG),
			{Type: "text", Text: "这张手写作文照片需要顺时针旋转多少度,文字才能正立(从左到右、从上到下正常阅读)?只回答一个数字:0、90、180 或 270。"},
		}}},
	})
	if err != nil {
		return 0, err
	}
	m := digitsRe.FindString(txt)
	deg, _ := strconv.Atoi(m)
	switch deg {
	case 0, 90, 180, 270:
		return deg, nil
	default:
		return 0, nil
	}
}

// Transcribe 把已经正立的单页作文图转写为连续中文。
func (l *LLM) Transcribe(pageJPEG []byte) (string, error) {
	txt, err := l.call(apiRequest{
		Model: l.Model, MaxTokens: 2000,
		System: "你是中文手写识别助手。把图片中的高中生手写作文逐字转写为通顺、连续的中文文字,保留分段。实在认不出的字用□代替。" +
			"严禁任何前言、说明、备注或评价——你的第一个字必须就是作文正文的第一个字,最后一个字就是作文正文的最后一个字。" +
			"即使图片方向不正或字迹不清,也直接输出转写正文,不要描述图片状态。",
		Messages: []apiMessage{{Role: "user", Content: []contentBlock{
			imageBlock(pageJPEG),
			{Type: "text", Text: "请转写这一页作文,直接输出正文。"},
		}}},
	})
	if err != nil {
		return "", err
	}
	return txt, nil // 返回原始文本;方向抱怨检测与清洗在 pipeline 里做
}

// orientationComplaint 判断转写结果里是否含"图片方向不对"的自述(说明方向错了,转写不可信)。
func orientationComplaint(s string) bool {
	for _, k := range []string{"倒置", "颠倒", "旋转", "方向不", "上下颠", "正常方向", "需要旋转", "侧着", "横放", "方向错"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// cleanTranscript 剥掉模型偶尔多嘴的开头前言("…转写如下:")和结尾备注("…建议重新拍摄"),
// 只留作文正文。
func cleanTranscript(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	meta := func(t string) bool {
		for _, k := range []string{"转写", "识别", "辨认", "倒置", "方向", "理解", "以下", "尽力", "如下", "图片", "原文", "无法", "建议", "可能存在", "重新拍"} {
			if strings.Contains(t, k) {
				return true
			}
		}
		return false
	}
	// 开头:丢弃以冒号结尾且像元说明的前言行。
	for len(lines) > 0 {
		t := strings.TrimSpace(lines[0])
		if t == "" {
			lines = lines[1:]
			continue
		}
		if (strings.HasSuffix(t, ":") || strings.HasSuffix(t, "：")) && meta(t) {
			lines = lines[1:]
			continue
		}
		break
	}
	// 结尾:丢弃像"备注/建议/无法辨认"的收尾行。
	for len(lines) > 0 {
		t := strings.TrimSpace(lines[len(lines)-1])
		if t == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		if meta(t) && (strings.Contains(t, "建议") || strings.Contains(t, "无法") || strings.Contains(t, "可能存在") || strings.Contains(t, "辨认困难") || strings.Contains(t, "重新拍") || strings.Contains(t, "如需")) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// GradeResult 评判阶段的结构化结果。
type GradeResult struct {
	Dimensions []DimensionScore `json:"dimensions"`
	Total      int              `json:"total"`
	Overall    string           `json:"overall"`
}

// Grade 基于题目 + 评分参考 + 维度 + 转写稿做分维度评分与评语。
func (l *LLM) Grade(prompt, guide string, dims []Dimension, transcript string, fullMarks int) (*GradeResult, error) {
	var db strings.Builder
	for _, d := range dims {
		fmt.Fprintf(&db, "- %s(满分 %d)\n", d.Name, d.Max)
	}
	guideBlock := ""
	if strings.TrimSpace(guide) != "" {
		guideBlock = fmt.Sprintf("\n【本题评分参考】(老师提供:材料解读 / 立意指导 / 思辨要求,务必据此把握立意尺度与评分标准)\n%s\n",
			strings.TrimSpace(guide))
	}
	user := fmt.Sprintf(`【作文题目/材料】
%s
%s
【评分维度】(各维度满分之和 = %d)
%s
【学生作文转写稿】(可能含识别误差,以达意为准)
%s

请严格按以下 JSON 输出(不要任何多余文字):
{"dimensions":[{"name":"维度名","max":满分整数,"score":得分整数,"comment":"该维度一句点评:点出问题+一句改进方向"}],"total":各维度得分之和整数,"overall":"总评2~4句"}
要求:维度与上面给定的完全一致;score 为整数且不超过该维度 max;total 等于各 score 之和;评分与点评须贴合上面的"评分参考"。`,
		strings.TrimSpace(prompt), guideBlock, fullMarks, db.String(), strings.TrimSpace(transcript))

	txt, err := l.call(apiRequest{
		Model: l.Model, MaxTokens: 1500,
		System: "你是经验丰富的高中语文阅卷老师,依据高考作文评分框架打分。你的产出是给老师参考的草稿,务必客观、具体。只输出 JSON。",
		Messages: []apiMessage{{Role: "user", Content: []contentBlock{{Type: "text", Text: user}}}},
	})
	if err != nil {
		return nil, err
	}
	var gr GradeResult
	if err := json.Unmarshal([]byte(extractJSON(txt)), &gr); err != nil {
		return nil, fmt.Errorf("评分结果解析失败: %v; 原文: %s", err, truncate(txt, 200))
	}
	// 兜底:total 取各维度之和,防止模型自相矛盾。
	sum := 0
	for _, d := range gr.Dimensions {
		sum += d.Score
	}
	gr.Total = sum
	return &gr, nil
}

// InsightResult 共性问题 + 讲评选篇(数值类洞察在 Go 侧算)。
type InsightResult struct {
	CommonIssues []string `json:"commonIssues"`
	Picks        []struct {
		Index  int    `json:"index"`
		Type   string `json:"type"`
		Reason string `json:"reason"`
	} `json:"picks"`
}

// AnalyzeBatch 纵览全班评语,归纳共性问题并挑选讲评篇目。
func (l *LLM) AnalyzeBatch(prompt, guide string, summaries string) (*InsightResult, error) {
	guideBlock := ""
	if strings.TrimSpace(guide) != "" {
		guideBlock = "\n本题评分参考(老师提供):\n" + strings.TrimSpace(guide) + "\n"
	}
	user := fmt.Sprintf(`本次作文题目:
%s
%s
以下是全班每篇作文的批改摘要(序号 / 总分 / 各维度得分 / 总评):
%s

请纵览全班,输出 JSON(不要多余文字):
{"commonIssues":["这批作文反复出现的共性问题,3~5条,每条具体可操作"],"picks":[{"index":作文序号整数,"type":"范文"或"典型问题","reason":"为何适合讲评,一句话"}]}
picks 选 2~4 篇:既要有可作范文的,也要有典型问题的。`,
		strings.TrimSpace(prompt), guideBlock, summaries)

	txt, err := l.call(apiRequest{
		Model: l.Model, MaxTokens: 1200,
		System: "你是语文教研组长,擅长从一批作文中提炼共性问题、为作文讲评课选材。只输出 JSON。",
		Messages: []apiMessage{{Role: "user", Content: []contentBlock{{Type: "text", Text: user}}}},
	})
	if err != nil {
		return nil, err
	}
	var ir InsightResult
	if err := json.Unmarshal([]byte(extractJSON(txt)), &ir); err != nil {
		return nil, fmt.Errorf("洞察解析失败: %v; 原文: %s", err, truncate(txt, 200))
	}
	return &ir, nil
}

// RecognizePrompt 从题目照片识别作文题目/材料文字。
func (l *LLM) RecognizePrompt(pageJPEG []byte) (string, error) {
	return l.call(apiRequest{
		Model: l.Model, MaxTokens: 1500,
		System: "你是中文文字识别助手。把图片中的作文题目/材料完整转写为文字,保留'要求'等条目。只输出题目文字,不要解释。",
		Messages: []apiMessage{{Role: "user", Content: []contentBlock{
			imageBlock(pageJPEG),
			{Type: "text", Text: "请转写这张作文题目。"},
		}}},
	})
}

// looksLikeFailedTranscription 判断转写结果是否其实是模型的"识别失败/拒绝"话术,
// 而非真正的作文正文。命中多个关键词才判定,避免误伤正常作文。
func looksLikeFailedTranscription(s string) bool {
	keys := []string{"无法识别", "无法辨认", "无法准确", "难以辨认", "倒置", "旋转了", "重新拍摄",
		"更清晰", "看不清", "无法完成转写", "无法转写", "建议提供", "辨认困难"}
	hit := 0
	for _, k := range keys {
		if strings.Contains(s, k) {
			hit++
		}
	}
	return hit >= 2
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// extractJSON 从模型输出里抓出**第一个完整且括号配平**的 JSON 对象。
// 比"第一个{到最后一个}"健壮:模型在 JSON 后面追加解释文字时不会把尾巴圈进来,
// 且能正确忽略字符串字面量内部的花括号。
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return s
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
