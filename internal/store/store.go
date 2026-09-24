// Package store 管理"模板库"（exe 同级目录下的 模板库\ 文件夹）：
// 用户选择保存的模板以 docx/xlsx + JSON 配置成对存放，下次直接调用。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry 模板库条目
type Entry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // docx | xlsx
	SavedAt string `json:"savedAt"`
}

// LibDir 模板库目录（便携模式：exe 同级 模板库\）
func LibDir() string {
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	return filepath.Join(filepath.Dir(exe), "模板库")
}

func safeName(name string) string {
	repl := func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}
	out := ""
	for _, r := range name {
		out += string(repl(r))
	}
	return out
}

// Save 保存模板文件与配置
func Save(name, kind string, tplBytes []byte, designJSON []byte) error {
	dir := LibDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base := safeName(name)
	tplPath := filepath.Join(dir, base+"."+kind)
	if err := os.WriteFile(tplPath, tplBytes, 0o644); err != nil {
		return err
	}
	meta := map[string]any{
		"name":    name,
		"kind":    kind,
		"design":  json.RawMessage(designJSON),
		"savedAt": time.Now().Format("2006-01-02 15:04"),
	}
	metaBytes, _ := json.Marshal(meta)
	return os.WriteFile(filepath.Join(dir, base+".json"), metaBytes, 0o644)
}

// List 列出模板库条目（按保存时间倒序）
func List() ([]Entry, error) {
	dir := LibDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // 目录不存在视为空库
	}
	var out []Entry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			SavedAt string `json:"savedAt"`
		}
		if json.Unmarshal(data, &m) != nil || m.Name == "" {
			continue
		}
		out = append(out, Entry{Name: m.Name, Kind: m.Kind, SavedAt: m.SavedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt > out[j].SavedAt })
	return out, nil
}

// Paths 返回指定模板的模板文件与配置文件路径
func Paths(name, kind string) (string, string) {
	base := filepath.Join(LibDir(), safeName(name))
	return base + "." + kind, base + ".json"
}

// LoadDesign 读取模板配置
func LoadDesign(name, kind string) ([]byte, error) {
	_, metaPath := Paths(name, kind)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("读取模板配置失败: %w", err)
	}
	var m struct {
		Design json.RawMessage `json:"design"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil, fmt.Errorf("模板配置解析失败")
	}
	return m.Design, nil
}
