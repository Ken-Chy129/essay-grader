package main

import (
	"fmt"
	"log"
	"strings"
)

// job 表示一次批改任务。mode=full 走"方向校正+转写+评分";mode=grade 只重跑评分(用于改完转写稿后重新批改)。
type job struct {
	essayID string
	mode    string // full | grade
}

// Pipeline 异步批改:后台 worker 逐篇处理,逐篇落盘,前端轮询即可看到进度。
type Pipeline struct {
	store *Store
	llm   *LLM
	jobs  chan job
}

func NewPipeline(store *Store, llm *LLM, workers int) *Pipeline {
	p := &Pipeline{store: store, llm: llm, jobs: make(chan job, 256)}
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

func (p *Pipeline) Enqueue(essayID, mode string) { p.jobs <- job{essayID, mode} }

func (p *Pipeline) worker() {
	for j := range p.jobs {
		p.process(j)
	}
}

func (p *Pipeline) setStatus(a *Assignment, e *Essay, status, errMsg string) {
	e.Status = status
	e.Error = errMsg
	if err := p.store.Put(a); err != nil {
		log.Printf("持久化失败 essay=%s: %v", e.ID, err)
	}
}

func (p *Pipeline) process(j job) {
	a, e := p.store.FindEssay(j.essayID)
	if a == nil || e == nil {
		log.Printf("找不到作文 %s", j.essayID)
		return
	}

	// 第一阶段:方向校正 + 逐页转写(grade 模式跳过,直接用现有/已编辑的转写稿)。
	if j.mode == "full" {
		p.setStatus(a, e, "transcribing", "")
		var parts []string
		for i, rel := range e.Images {
			text, err := p.transcribePage(rel)
			if err != nil {
				p.setStatus(a, e, "error", fmt.Sprintf("第%d页转写失败: %v", i+1, err))
				return
			}
			parts = append(parts, strings.TrimSpace(text))
		}
		e.Transcript = strings.Join(parts, "\n\n")
	}

	if strings.TrimSpace(e.Transcript) == "" {
		p.setStatus(a, e, "error", "转写稿为空,无法批改")
		return
	}

	// 第二阶段:评分 + 评语。
	p.setStatus(a, e, "grading", "")
	res, err := p.llm.Grade(a.Prompt, a.Guide, a.Dimensions, e.Transcript, a.FullMarks)
	if err != nil {
		p.setStatus(a, e, "error", fmt.Sprintf("评分失败: %v", err))
		return
	}
	e.Scores = res.Dimensions
	e.Total = res.Total
	e.Overall = res.Overall
	p.setStatus(a, e, "done", "")
}

// transcribePage 对一张**已由 EXIF/人工确认为正立**的页面图直接转写。
// LLM 不再关心方向(方向由工程 EXIF + 老师手动旋转把关)。仅保留失败兜底:
// 万一仍读不出或自述方向不对,判失败、绝不把可疑文本送去评分。
func (p *Pipeline) transcribePage(rel string) (string, error) {
	raw, err := p.store.ReadUpload(rel)
	if err != nil {
		return "", err
	}
	img, err := decodeImage(raw)
	if err != nil {
		return "", fmt.Errorf("图片解码失败: %v", err)
	}
	page, err := encodeJPEG(downscale(img, 1600), 90)
	if err != nil {
		return "", err
	}
	text, err := p.llm.Transcribe(page)
	if err != nil {
		return "", err
	}
	if orientationComplaint(text) || looksLikeFailedTranscription(text) {
		return "", fmt.Errorf("识别失败:照片可能方向不对或字迹不清,请用“↻ 旋转”校正后重试")
	}
	return cleanTranscript(text), nil
}
