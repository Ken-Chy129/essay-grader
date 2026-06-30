package main

// Dimension 是一条评分维度的定义(模板)。
type Dimension struct {
	Name string `json:"name"`
	Max  int    `json:"max"`
}

// DimensionScore 是某篇作文在某维度上的实际得分与点评。
type DimensionScore struct {
	Name    string `json:"name"`
	Max     int    `json:"max"`
	Score   int    `json:"score"`
	Comment string `json:"comment"`
}

// Essay 一篇学生作文:一组有序页面照片 + 两阶段批改结果。
type Essay struct {
	ID         string           `json:"id"`
	Label      string           `json:"label"` // 老师给的标识(如学号/姓名/序号),可空
	Images     []string         `json:"images"`
	Status     string           `json:"status"` // uploaded|pending|transcribing|grading|done|error
	Rev        int              `json:"rev"`    // 旋转次数,用于前端缩略图缓存刷新
	Error      string           `json:"error,omitempty"`
	Transcript string           `json:"transcript"`
	Scores     []DimensionScore `json:"scores"`
	Total      int              `json:"total"`
	Overall    string           `json:"overall"`
	CreatedAt  string           `json:"createdAt"`
}

// Insights 整批跑完后的数据洞察(D)。
type Insights struct {
	Count        int            `json:"count"`
	Avg          float64        `json:"avg"`
	Max          int            `json:"max"`
	Min          int            `json:"min"`
	Distribution map[string]int `json:"distribution"` // 分段 -> 人数
	WeakestDim   string         `json:"weakestDim"`
	DimAvg       map[string]float64 `json:"dimAvg"`
	CommonIssues []string       `json:"commonIssues"`
	Picks        []Pick         `json:"picks"`
	GeneratedAt  string         `json:"generatedAt"`
}

// Pick 讲评选篇:挑出的范文 / 典型问题作文。
type Pick struct {
	EssayID string `json:"essayId"`
	Label   string `json:"label"`
	Type    string `json:"type"` // 范文 | 典型问题
	Reason  string `json:"reason"`
}

// Assignment 一次作业:题目 + 一批作文 + 维度模板 + 洞察。
type Assignment struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Prompt     string      `json:"prompt"`
	Guide      string      `json:"guide"` // 老师提供的评分参考:材料解读 / 立意指导 / 思辨要求等
	FullMarks  int         `json:"fullMarks"`
	Dimensions []Dimension `json:"dimensions"`
	Essays     []*Essay    `json:"essays"`
	Insights   *Insights   `json:"insights,omitempty"`
	CreatedAt  string      `json:"createdAt"`
}

// DefaultDimensions 是 CONTEXT.md 里定的默认维度模板:立意/结构/语言/内容 各 15 分。
func DefaultDimensions() []Dimension {
	return []Dimension{
		{Name: "立意", Max: 15},
		{Name: "结构", Max: 15},
		{Name: "语言", Max: 15},
		{Name: "内容", Max: 15},
	}
}
