// Package langdet 用纯规则判断输入文本是否为"可纠正的英文短句"。
// 打字路径：不含 CJK、ASCII 字母占比达标、词数与长度在阈值内、以句末标点结尾。
// 选中/剪贴板路径（CheckSelection）：不检查标点，英文字母占比达标走纠正，否则按中文走翻译。
package langdet

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Rules 是英文短句判定阈值。
type Rules struct {
	MaxWords      int     // 最大词数（超出视为长句，不纠正）
	MaxChars      int     // 最大字符数（超出视为长句，不纠正）
	MinWords      int     // 最小词数（少于视为无意义片段，不纠正）
	MinKnownRatio float64 // 已知词占比阈值（<=0 时关闭词典过滤）
	MinLangRatio  float64 // 语言门槛：英文占比达此门槛走纠正、否则按中文翻译（<=0 时关闭）
}

// DefaultRules 返回默认阈值。
func DefaultRules() Rules {
	return Rules{MaxWords: 40, MaxChars: 300, MinWords: 2, MinKnownRatio: 0.3, MinLangRatio: 0.6}
}

// Verdict 是判定结果。
type Verdict int

const (
	// Reject 表示不满足条件，跳过（含中文、太短、太长、非英文等）。
	Reject Verdict = iota
	// Pending 表示英文但不完整（尚未以 .!? 结尾），等待继续输入。
	Pending
	// Correct 表示完整的英文短句，可送 LLM 纠正。
	Correct
)

// sentenceEnd 是句子结束标点（包含中英文）。
var sentenceEnd = map[rune]bool{
	'.': true, '!': true, '?': true,
	'。': true, '！': true, '？': true,
	';': true, '；': true,
}

// ExtractSentence 从全文提取"当前正在输入的句子"：
// 最后一个句末标点/换行之后仍有内容 → 返回该进行中的片段；
// 句子以标点结尾（刚打完标点）→ 返回最后一个完整句（含结尾标点）。
func ExtractSentence(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return ""
	}
	lastSep, prevSep, lastSepLen := -1, -1, 0
	prevSepLen := 0
	for i, r := range full {
		if sentenceEnd[r] || r == '\n' || r == '\t' || r == '\r' {
			prevSep = lastSep
			prevSepLen = lastSepLen
			lastSep = i
			lastSepLen = len(string(r))
		}
	}
	if lastSep == -1 {
		return full // 尚无任何标点，视为进行中的句子
	}
	if after := strings.TrimSpace(full[lastSep+lastSepLen:]); after != "" {
		return after // 进行中的句子
	}
	// 句子以标点结尾：取 prevSep 之后（含末尾标点）的完整句。
	start := 0
	if prevSep >= 0 {
		start = prevSep + prevSepLen
		for start < len(full) && (full[start] == ' ' || full[start] == '\n' || full[start] == '\t') {
			start++
		}
	}
	return strings.TrimSpace(full[start:])
}

// HasCJK 判断文本是否含中日韩字符。
func HasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// latinRatio 计算 ASCII 字母占"可打印非空白字符"的比例。
func latinRatio(s string) float64 {
	var latin, printable int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		printable++
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			latin++
		}
	}
	if printable == 0 {
		return 0
	}
	return float64(latin) / float64(printable)
}

// wordCount 按空白切分的 token 数（非空的）。
func wordCount(s string) int {
	return len(strings.Fields(s))
}

// wordTrim 是查词典前剥离的首尾标点。
var wordTrim = ".,!?;:()[]{}'\"“”‘’…—-—"

// knownRatio 计算命中内置高频词库的词占比：用于拦截垃圾串、代码、
// 非英语拉丁文本（如德语）。容忍拼写错误——功能词几乎不会拼错。
func knownRatio(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	var known int
	for _, w := range fields {
		w = strings.ToLower(strings.Trim(w, wordTrim))
		if w == "" {
			continue
		}
		if _, ok := commonWords[w]; ok {
			known++
		}
	}
	return float64(known) / float64(len(fields))
}

// hanRatio 计算汉字占"可打印非空白字符"的比例。
func hanRatio(s string) float64 {
	var han, printable int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		printable++
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	if printable == 0 {
		return 0
	}
	return float64(han) / float64(printable)
}

// HanRatio 计算汉字占"可打印非空白字符"的比例（导出版本，供触发分流使用）。
func HanRatio(s string) float64 {
	return hanRatio(s)
}

// IsChinese 判断文本汉字占比是否达到门槛（中文场景分流）。
func IsChinese(s string, minRatio float64) bool {
	if minRatio <= 0 {
		return false
	}
	return hanRatio(s) >= minRatio
}

// IsLatin 判断文本英文字母占比是否达到门槛（英文场景分流）。
func IsLatin(s string, minRatio float64) bool {
	if minRatio <= 0 {
		return false
	}
	return latinRatio(s) >= minRatio
}

// IsComplete 判断句子是否以英文句末标点结尾（视为完整句）。
func IsComplete(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case '.', '!', '?':
		return true
	}
	return false
}

// Check 对"当前句子"做完整判定（要求句末标点）。
func Check(sentence string, r Rules) Verdict {
	v := CheckFragment(sentence, r)
	if v != Correct {
		return v
	}
	// 完整性判定：未打完的句子等待。
	if !IsComplete(sentence) {
		return Pending
	}
	return Correct
}

// CheckFragment 判定文本是否为可纠正的英文（不要求句末标点），供选中触发用。
func CheckFragment(sentence string, r Rules) Verdict {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return Reject
	}
	// 语言判定：含 CJK 一律跳过；ASCII 字母占比不足（纯数字/符号）跳过。
	if HasCJK(sentence) {
		return Reject
	}
	if latinRatio(sentence) < 0.6 {
		return Reject
	}
	// 词典软过滤：已知词占比不足视为非英文/垃圾串（r.MinKnownRatio <= 0 时关闭）。
	if r.MinKnownRatio > 0 && knownRatio(sentence) < r.MinKnownRatio {
		return Reject
	}
	// 短句判定：过长不纠正。
	if len(sentence) > r.MaxChars {
		return Reject
	}
	words := wordCount(sentence)
	if words < r.MinWords {
		return Reject
	}
	if words > r.MaxWords {
		return Reject
	}
	return Correct
}

// CheckSelection 判定选中/剪贴板文本是否可触发（英/中均可）：不检查标点、不查词典。
// 语言分流由调用方按 IsLatin 决定：英文字母占比达标走纠正，否则一律按中文
// 走中译英（中英混合也触发）。完全不含汉字与英文字母的纯符号/数字串被拒。
// 长度与词数仍受阈值约束。
func CheckSelection(s string, r Rules) Verdict {
	s = strings.TrimSpace(s)
	if s == "" {
		return Reject
	}
	// 语言判定：不含任何汉字与 ASCII 字母（纯符号/数字/其他文字）不触发。
	if r.MinLangRatio > 0 && hanRatio(s) == 0 && latinRatio(s) == 0 {
		return Reject
	}
	if len(s) > r.MaxChars {
		return Reject
	}
	words := wordCount(s)
	if words < r.MinWords {
		return Reject
	}
	if words > r.MaxWords {
		return Reject
	}
	return Correct
}

// Hash 计算句子指纹，用于去重。
func Hash(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:8])
}
