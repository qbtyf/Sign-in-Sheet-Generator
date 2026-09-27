// Package bilingual 内置模板标签的双语替换引擎（V3.0）。
//
// 词典来源：web/locales/*.json 中 "label." 前缀的词条
// （键为模板中的中文原文，值为对应语言的译文；zh-CN 包中键=值）。
//
// 替换规则：对 document.xml 中每个 <w:t> 的文本做【整词完全匹配】，
// 命中词典且不含 { }（排除占位符）时替换为 "译文1 / 译文2"（同行斜杠版式）。
// 整词匹配天然避免子串误伤（如"参加单位"不会被"单位"规则破坏）。
package bilingual

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Dict 中文原文 → (语言代码 → 译文)
type Dict map[string]map[string]string

// reT 匹配单个 <w:t ...>文本</w:t> 元素
var reT = regexp.MustCompile(`(<w:t[^>]*>)([^<]*)(</w:t>)`)

// Load 从 locales 目录加载全部语言包，构建 label.* 词典。
// 返回的 Dict 键为中文原文（"label." 后的部分）。
func Load(locales fs.FS) (Dict, error) {
	d := Dict{}
	entries, err := fs.ReadDir(locales, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		raw, err := fs.ReadFile(locales, path.Join(".", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取语言包 %s: %w", e.Name(), err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("解析语言包 %s: %w", e.Name(), err)
		}
		for k, v := range m {
			if !strings.HasPrefix(k, "label.") {
				continue
			}
			cn := strings.TrimPrefix(k, "label.")
			if d[cn] == nil {
				d[cn] = map[string]string{}
			}
			d[cn][lang] = v
		}
	}
	if len(d) == 0 {
		return nil, fmt.Errorf("语言包中未找到 label.* 词条")
	}
	return d, nil
}

// Keys 返回词典全部中文原文（按长度降序，便于调试/测试展示）
func (d Dict) Keys() []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// Apply 对 OOXML 字符串做双语标签替换。
// lang1/lang2 为语言代码；zh-CN 表示保留中文原文。
// 返回原串的条件：dict 为空或两个语言相同。
func Apply(xmlStr string, dict Dict, lang1, lang2 string) string {
	if dict == nil || lang1 == "" || lang2 == "" || lang1 == lang2 {
		return xmlStr
	}
	return reT.ReplaceAllStringFunc(xmlStr, func(m string) string {
		g := reT.FindStringSubmatch(m)
		open, raw, close := g[1], g[2], g[3]
		text := html.UnescapeString(raw)
		if text == "" || strings.ContainsAny(text, "{}") {
			return m // 占位符或空文本不动
		}
		tr, ok := dict[text]
		if !ok {
			return m // 整词未命中词典，原样保留（含用户数据/数字等）
		}
		t1, ok1 := tr[lang1]
		if !ok1 {
			return m // 该语言缺此词条，保守起见原样保留
		}
		t2, ok2 := tr[lang2]
		if !ok2 {
			return m
		}
		if lang1 == "zh-CN" {
			t1 = text
		}
		if lang2 == "zh-CN" {
			t2 = text
		}
		return open + esc(t1+" / "+t2) + close
	})
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
