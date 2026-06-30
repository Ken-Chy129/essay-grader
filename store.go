package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Store 用 JSON 文件持久化作业(单用户本地工具,数据量小,全量内存 + 落盘即可)。
type Store struct {
	mu   sync.RWMutex
	dir  string // data 根目录
	byID map[string]*Assignment
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "assignments"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "uploads"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, byID: map[string]*Assignment{}}
	entries, _ := os.ReadDir(filepath.Join(dir, "assignments"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "assignments", e.Name()))
		if err != nil {
			continue
		}
		var a Assignment
		if json.Unmarshal(b, &a) == nil && a.ID != "" {
			s.byID[a.ID] = &a
		}
	}
	return s, nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowStr() string { return time.Now().Format(time.RFC3339) }

func (s *Store) List() []*Assignment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Assignment, 0, len(s.byID))
	for _, a := range s.byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (s *Store) Get(id string) *Assignment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID[id]
}

// FindEssay 跨作业按 essayID 定位。
func (s *Store) FindEssay(essayID string) (*Assignment, *Essay) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.byID {
		for _, e := range a.Essays {
			if e.ID == essayID {
				return a, e
			}
		}
	}
	return nil, nil
}

// Put 保存并落盘。
func (s *Store) Put(a *Assignment) error {
	s.mu.Lock()
	s.byID[a.ID] = a
	s.mu.Unlock()
	return s.persist(a)
}

func (s *Store) persist(a *Assignment) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "assignments", a.ID+".json"), b, 0o644)
}

// SaveUpload 保存一篇作文的一张页面图,返回相对路径(用于 /uploads/ 访问)。
func (s *Store) SaveUpload(essayID string, idx int, data []byte) (string, error) {
	dir := filepath.Join(s.dir, "uploads", essayID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d.img", idx)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return filepath.Join("uploads", essayID, name), nil
}

func (s *Store) ReadUpload(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.dir, rel))
}

// ThumbFor 返回某张图按长边 maxDim 缩放的 JPEG,按尺寸分别磁盘缓存;
// 原图更新(如旋转,mtime 变新)时自动重新生成。
func (s *Store) ThumbFor(rel string, maxDim int) ([]byte, error) {
	main := filepath.Join(s.dir, rel)
	mi, err := os.Stat(main)
	if err != nil {
		return nil, err
	}
	thumb := main + ".thumb" + strconv.Itoa(maxDim)
	if ti, e := os.Stat(thumb); e == nil && !ti.ModTime().Before(mi.ModTime()) {
		return os.ReadFile(thumb)
	}
	raw, err := os.ReadFile(main)
	if err != nil {
		return nil, err
	}
	img, err := decodeImage(raw)
	if err != nil {
		return nil, err
	}
	b, err := encodeJPEG(downscale(img, maxDim), 80)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(thumb, b, 0o644)
	return b, nil
}

// WriteUpload 覆盖写回某张已存在的页面图(供"旋转"使用)。
func (s *Store) WriteUpload(rel string, data []byte) error {
	return os.WriteFile(filepath.Join(s.dir, rel), data, 0o644)
}

// DeleteAssignment 删除整份作业:内存 + JSON 文件 + 其下所有作文的图片目录。
func (s *Store) DeleteAssignment(id string) error {
	s.mu.Lock()
	a := s.byID[id]
	delete(s.byID, id)
	s.mu.Unlock()
	if a != nil {
		for _, e := range a.Essays {
			_ = os.RemoveAll(filepath.Join(s.dir, "uploads", e.ID))
		}
	}
	return os.Remove(filepath.Join(s.dir, "assignments", id+".json"))
}

// DeleteEssay 从所属作业里删除一篇作文,并清理其图片目录。
func (s *Store) DeleteEssay(essayID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.byID {
		for i, e := range a.Essays {
			if e.ID == essayID {
				a.Essays = append(a.Essays[:i], a.Essays[i+1:]...)
				a.Insights = nil
				_ = os.RemoveAll(filepath.Join(s.dir, "uploads", essayID))
				_ = s.persist(a)
				return true
			}
		}
	}
	return false
}
