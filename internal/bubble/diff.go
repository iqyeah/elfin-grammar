// Package bubble 的共享差异计算：对原文与纠正文做词级对齐，
// 产出纠正文中变化词所在的区间（供各平台把差异词标红）。
package bubble

import (
	"strings"
	"unicode/utf16"
)

// Span 是显示文本中的一个区间（UTF-16 码元单位，与 macOS 的 NSRange、
// Windows 的 wchar_t* 偏移单位一致），用于标记纠正文本中的差异词。
type Span struct {
	Start  int
	Length int
}

// DiffSpans 对原文与纠正文做词级 LCS 对齐，返回纠正文中变化词的区间列表。
// 相邻变化词合并为同一区间（含其间空白），观感上是整段红色而非逐词碎红。
// 归一化仅去除首尾标点（保留大小写差异：如 i→I 属真实纠正应标红）。
// 原文或纠正文为空时返回 nil（翻译/错误文案等无对比意义的场景）。
func DiffSpans(original, corrected string) []Span {
	a := tokenizeWords(original)
	b := tokenizeWords(corrected)
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	matched := lcsMatch(a, b)
	var spans []Span
	j := 0
	for j < len(b) {
		if matched[j] {
			j++
			continue
		}
		k := j
		for k+1 < len(b) && !matched[k+1] {
			k++
		}
		start := utf16Len(corrected[:b[j].start])
		end := utf16Len(corrected[:b[k].end])
		spans = append(spans, Span{Start: start, Length: end - start})
		j = k + 1
	}
	return spans
}

// wordTok 是切词结果：归一化词形（去首尾标点）与字节区间。
type wordTok struct {
	norm       string
	start, end int
}

// tokenizeWords 按空白切词（标点吸附在词上），返回词表（不含空白）。
func tokenizeWords(s string) []wordTok {
	var toks []wordTok
	i := 0
	for i < len(s) {
		if isSpaceByte(s[i]) {
			i++
			continue
		}
		start := i
		for i < len(s) && !isSpaceByte(s[i]) {
			i++
		}
		toks = append(toks, wordTok{norm: normalizeWord(s[start:i]), start: start, end: i})
	}
	return toks
}

func isSpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// normalizeWord 归一化词形：去掉首尾标点后参与比较。
// 仅标点差异（如 school.→school）不算变化词，避免误标红。
func normalizeWord(w string) string {
	return strings.Trim(w, ".,;:!?\"'“”‘’()[]{}—…–-")
}

// lcsMatch 按归一化词形求最长公共子序列，返回 corrected 侧参与匹配的词下标集合。
func lcsMatch(a, b []wordTok) []bool {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i].norm == b[j].norm {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	matched := make([]bool, m)
	i, j := 0, 0
	for i < n && j < m {
		if a[i].norm == b[j].norm {
			matched[j] = true
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return matched
}

// utf16Len 返回 s 的 UTF-16 码元长度（词边界均为 rune 边界，无中间截断）。
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
