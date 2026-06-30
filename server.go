package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type Server struct {
	store    *Store
	llm      *LLM
	pipe     *Pipeline
	web      http.Handler
	password string // 留空=不鉴权
}

func NewServer(store *Store, llm *LLM, pipe *Pipeline, webDir, password string) *Server {
	return &Server{store: store, llm: llm, pipe: pipe, web: http.FileServer(http.Dir(webDir)), password: password}
}

const sessionCookie = "eg_session"

func sessionToken(pw string) string {
	sum := sha256.Sum256([]byte("essay-grader|v1|" + pw))
	return hex.EncodeToString(sum[:])
}

func (s *Server) isAuthed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(sessionToken(s.password))) == 1
}

// auth 中间件:静态资源与登录接口放行,其余(API/图片)需会话 Cookie。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.password == "" {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		open := p == "/" || p == "/index.html" || p == "/app.js" || p == "/style.css" || p == "/favicon.ico" ||
			(r.Method == "POST" && p == "/api/login")
		if open || s.isAuthed(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, 401, "未登录")
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if s.password == "" || subtle.ConstantTimeCompare([]byte(in.Password), []byte(s.password)) != 1 {
		writeErr(w, 401, "密码错误")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sessionToken(s.password), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/assignments", s.listAssignments)
	mux.HandleFunc("POST /api/assignments", s.createAssignment)
	mux.HandleFunc("GET /api/assignments/{id}", s.getAssignment)
	mux.HandleFunc("DELETE /api/assignments/{id}", s.deleteAssignment)
	mux.HandleFunc("DELETE /api/essays/{id}", s.deleteEssay)
	mux.HandleFunc("POST /api/assignments/{id}/essays", s.addEssay)
	mux.HandleFunc("POST /api/assignments/{id}/essays-batch", s.addEssaysBatch)
	mux.HandleFunc("POST /api/assignments/{id}/grade", s.gradeAll)
	mux.HandleFunc("POST /api/assignments/{id}/insights", s.computeInsights)
	mux.HandleFunc("POST /api/recognize-prompt", s.recognizePrompt)
	mux.HandleFunc("PUT /api/essays/{id}/transcript", s.editTranscript)
	mux.HandleFunc("POST /api/essays/{id}/rotate", s.rotateEssay)
	mux.HandleFunc("POST /api/essays/{id}/regrade", s.regrade)
	mux.Handle("/uploads/", s.uploadsHandler())
	mux.HandleFunc("GET /thumb/", s.thumbHandler)
	mux.Handle("/", s.noCacheStatic(s.web))
	return s.auth(mux)
}

// noCacheStatic 让 HTML/JS/CSS 始终向服务器校验(变了才重下,没变 304),
// 这样每次部署后普通刷新即可拿到新前端,无需强制硬刷新。
func (s *Server) noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) uploadsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if strings.Contains(rel, "..") {
			http.NotFound(w, r)
			return
		}
		b, err := s.store.ReadUpload(rel)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// 上传的是原始照片(png/jpeg),浏览器按内容嗅探即可。
		w.Header().Set("content-type", http.DetectContentType(b))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	})
}

// thumbHandler 提供列表用的小缩略图(几十 KB),大幅减少加载量。
func (s *Server) thumbHandler(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/thumb/")
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	maxDim := 360
	if q := r.URL.Query().Get("w"); q != "" {
		if n, e := strconv.Atoi(q); e == nil && n >= 80 && n <= 2000 {
			maxDim = n
		}
	}
	b, err := s.store.ThumbFor(rel, maxDim)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}

func (s *Server) listAssignments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.store.List())
}

func (s *Server) createAssignment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title      string      `json:"title"`
		Prompt     string      `json:"prompt"`
		Guide      string      `json:"guide"`
		FullMarks  int         `json:"fullMarks"`
		Dimensions []Dimension `json:"dimensions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if strings.TrimSpace(in.Prompt) == "" {
		writeErr(w, 400, "题目不能为空")
		return
	}
	if in.FullMarks <= 0 {
		in.FullMarks = 60
	}
	if len(in.Dimensions) == 0 {
		in.Dimensions = DefaultDimensions()
	}
	a := &Assignment{
		ID: newID(), Title: strings.TrimSpace(in.Title), Prompt: in.Prompt, Guide: strings.TrimSpace(in.Guide),
		FullMarks: in.FullMarks, Dimensions: in.Dimensions, Essays: []*Essay{}, CreatedAt: nowStr(),
	}
	if a.Title == "" {
		a.Title = "未命名作业"
	}
	if err := s.store.Put(a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) getAssignment(w http.ResponseWriter, r *http.Request) {
	a := s.store.Get(r.PathValue("id"))
	if a == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) deleteAssignment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.store.Get(id) == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	if err := s.store.DeleteAssignment(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteEssay(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteEssay(r.PathValue("id")) {
		writeErr(w, 404, "作文不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// addEssay 上传一篇作文(1~N 张页面图)。只上传、EXIF 转正,**不批改**(状态=uploaded)。
func (s *Server) addEssay(w http.ResponseWriter, r *http.Request) {
	a := s.store.Get(r.PathValue("id"))
	if a == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		writeErr(w, 400, "上传解析失败: "+err.Error())
		return
	}
	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		writeErr(w, 400, "未收到图片")
		return
	}
	e := &Essay{ID: newID(), Label: strings.TrimSpace(r.FormValue("label")), Status: "uploaded", CreatedAt: nowStr()}
	for i, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeErr(w, 400, "读取图片失败")
			return
		}
		data, _ := io.ReadAll(f)
		f.Close()
		rel, err := s.store.SaveUpload(e.ID, i, normalizeUpload(data))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		e.Images = append(e.Images, rel)
	}
	a.Essays = append(a.Essays, e)
	a.Insights = nil // 新增作文后旧洞察失效
	if err := s.store.Put(a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, e)
}

// addEssaysBatch 批量上传:每张照片各成一篇,EXIF 转正后存为"待评分"(uploaded),**不批改**。
func (s *Server) addEssaysBatch(w http.ResponseWriter, r *http.Request) {
	a := s.store.Get(r.PathValue("id"))
	if a == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeErr(w, 400, "上传解析失败: "+err.Error())
		return
	}
	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		writeErr(w, 400, "未收到图片")
		return
	}
	var created []*Essay
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(f)
		f.Close()
		e := &Essay{ID: newID(), Label: fileStem(fh.Filename), Status: "uploaded", CreatedAt: nowStr()}
		rel, err := s.store.SaveUpload(e.ID, 0, normalizeUpload(data))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		e.Images = []string{rel}
		a.Essays = append(a.Essays, e)
		created = append(created, e)
	}
	a.Insights = nil
	if err := s.store.Put(a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, created)
}

// gradeAll 把作业里所有"待评分/出错"的作文入队批改(老师确认方向无误后点"开始评分")。
func (s *Server) gradeAll(w http.ResponseWriter, r *http.Request) {
	a := s.store.Get(r.PathValue("id"))
	if a == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	n := 0
	for _, e := range a.Essays {
		if e.Status == "uploaded" || e.Status == "error" {
			e.Status = "pending"
			e.Error = ""
			n++
		}
	}
	if n == 0 {
		writeErr(w, 400, "没有待评分的作文")
		return
	}
	_ = s.store.Put(a)
	for _, e := range a.Essays {
		if e.Status == "pending" {
			s.pipe.Enqueue(e.ID, "full")
		}
	}
	writeJSON(w, 200, map[string]int{"queued": n})
}

// rotateEssay 把作文的页面图顺时针旋转 90°(纯图像,人工方向把关),并重置为"待评分"。
func (s *Server) rotateEssay(w http.ResponseWriter, r *http.Request) {
	a, e := s.store.FindEssay(r.PathValue("id"))
	if e == nil {
		writeErr(w, 404, "作文不存在")
		return
	}
	for _, rel := range e.Images {
		raw, err := s.store.ReadUpload(rel)
		if err != nil {
			continue
		}
		img, err := decodeImage(raw)
		if err != nil {
			continue
		}
		out, err := encodeJPEG(rotateCW(img, 90), 92)
		if err != nil {
			continue
		}
		_ = s.store.WriteUpload(rel, out)
	}
	// 旋转后旧的转写/评分失效,回到待评分。
	e.Status = "uploaded"
	e.Error = ""
	e.Transcript = ""
	e.Scores = nil
	e.Total = 0
	e.Overall = ""
	e.Rev++
	a.Insights = nil
	if err := s.store.Put(a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, e)
}

// fileStem 去掉文件名的扩展名,用作作文默认标识。
func fileStem(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return name
}

func (s *Server) editTranscript(w http.ResponseWriter, r *http.Request) {
	a, e := s.store.FindEssay(r.PathValue("id"))
	if e == nil {
		writeErr(w, 404, "作文不存在")
		return
	}
	var in struct {
		Transcript string `json:"transcript"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	e.Transcript = in.Transcript
	if err := s.store.Put(a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, e)
}

// regrade 用当前(可能已编辑的)转写稿重新评分,不重新识图。
func (s *Server) regrade(w http.ResponseWriter, r *http.Request) {
	a, e := s.store.FindEssay(r.PathValue("id"))
	if e == nil {
		writeErr(w, 404, "作文不存在")
		return
	}
	e.Status = "pending"
	e.Error = ""
	_ = s.store.Put(a)
	s.pipe.Enqueue(e.ID, "grade")
	writeJSON(w, 200, e)
}

func (s *Server) recognizePrompt(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, 400, "上传解析失败")
		return
	}
	fhs := r.MultipartForm.File["image"]
	if len(fhs) == 0 {
		writeErr(w, 400, "未收到图片")
		return
	}
	f, _ := fhs[0].Open()
	data, _ := io.ReadAll(f)
	f.Close()
	img, err := decodeImage(normalizeUpload(data)) // EXIF 转正
	if err != nil {
		writeErr(w, 400, "图片解码失败")
		return
	}
	page, _ := encodeJPEG(downscale(img, 1600), 90)
	text, err := s.llm.RecognizePrompt(page)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"prompt": strings.TrimSpace(text)})
}

// computeInsights 整批跑完后计算 D:数值在 Go 侧算,共性问题/选篇调模型。
func (s *Server) computeInsights(w http.ResponseWriter, r *http.Request) {
	a := s.store.Get(r.PathValue("id"))
	if a == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	var done []*Essay
	for _, e := range a.Essays {
		if e.Status == "done" {
			done = append(done, e)
		}
	}
	if len(done) == 0 {
		writeErr(w, 400, "还没有已完成批改的作文")
		return
	}

	ins := &Insights{Count: len(done), Distribution: map[string]int{}, DimAvg: map[string]float64{}, GeneratedAt: nowStr()}
	ins.Min = done[0].Total
	sumTotal := 0
	dimSum := map[string]int{}
	for _, e := range done {
		sumTotal += e.Total
		if e.Total > ins.Max {
			ins.Max = e.Total
		}
		if e.Total < ins.Min {
			ins.Min = e.Total
		}
		ins.Distribution[band(e.Total, a.FullMarks)]++
		for _, d := range e.Scores {
			dimSum[d.Name] += d.Score
		}
	}
	ins.Avg = float64(sumTotal) / float64(len(done))

	// 维度短板:平均得分率最低的维度。
	worstRatio := 2.0
	for _, dim := range a.Dimensions {
		avg := float64(dimSum[dim.Name]) / float64(len(done))
		ins.DimAvg[dim.Name] = avg
		ratio := 1.0
		if dim.Max > 0 {
			ratio = avg / float64(dim.Max)
		}
		if ratio < worstRatio {
			worstRatio = ratio
			ins.WeakestDim = dim.Name
		}
	}

	// 共性问题 + 讲评选篇:交给模型纵览。
	var sb strings.Builder
	for i, e := range done {
		fmt.Fprintf(&sb, "#%d 总分%d/%d;", i+1, e.Total, a.FullMarks)
		for _, d := range e.Scores {
			fmt.Fprintf(&sb, "%s%d ", d.Name, d.Score)
		}
		fmt.Fprintf(&sb, ";总评:%s\n", strings.TrimSpace(e.Overall))
	}
	if ir, err := s.llm.AnalyzeBatch(a.Prompt, a.Guide, sb.String()); err == nil {
		ins.CommonIssues = ir.CommonIssues
		for _, p := range ir.Picks {
			if p.Index >= 1 && p.Index <= len(done) {
				e := done[p.Index-1]
				ins.Picks = append(ins.Picks, Pick{EssayID: e.ID, Label: e.Label, Type: p.Type, Reason: p.Reason})
			}
		}
	} else {
		ins.CommonIssues = []string{"(共性问题归纳失败:" + err.Error() + ")"}
	}

	a.Insights = ins
	_ = s.store.Put(a)
	writeJSON(w, 200, ins)
}

// band 按满分占比给分段标签。
//nolint
func band(total, full int) string {
	if full <= 0 {
		full = 60
	}
	r := float64(total) / float64(full)
	switch {
	case r >= 0.85:
		return "优(85%+)"
	case r >= 0.70:
		return "良(70-85%)"
	case r >= 0.60:
		return "中(60-70%)"
	default:
		return "待提升(<60%)"
	}
}
