package main

import "strings"

// ------------------------------------------------------------ 单元格转义机制
//
// list 用 | 分隔、struct/map 用 ; 和 : 分隔。为了在值里写这些字符本身，
// 支持反斜杠转义: \\ 表示 \，\| 表示 |，\; 表示 ;，\: 表示 :。
//
// 实现分两步，与解析层级对应:
//   - 切分层（splitUnescaped/splitKVUnescaped）: 只在「未转义」的分隔符处切开，
//     转义序列原样留在片段里——这样内层（如 struct 字段值里的 list）仍能看到
//     属于自己的转义，不会被外层提前吃掉。
//   - 叶子层（unescapeCell）: 最终标量值（string、map 的 string 键）做反转义。
//
// 不含反斜杠的单元格行为与旧版完全一致；未列出的 \x 序列原样保留（宽容策略，
// 兼容 Windows 路径之类的既有写法）。

// escapableChars 可被 \ 转义的字符
const escapableChars = "\\|;:"

// splitUnescaped 在未转义的 sep 处切分。转义序列（\x 两字符）原样保留在片段中，
// 供内层或叶子值继续处理。注意 \\ 是已配对的转义，其后的分隔符照常生效。
func splitUnescaped(s string, sep byte) []string {
	var out []string
	var b strings.Builder
	b.Grow(len(s))
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			b.WriteByte(c)
			escaped = true
			continue
		}
		if c == sep {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	out = append(out, b.String())
	return out
}

// splitKVUnescaped 在第一个未转义的 sep 处一分为二（struct/map 的 键:值 切分）。
// 返回 false 表示没有未转义的分隔符。
func splitKVUnescaped(s string, sep byte) (string, string, bool) {
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// unescapeCell 反转义叶子值: \\ \| \; \: 还原为 \ | ; :；
// 其余 \x 序列原样保留；末尾孤立的 \ 也原样保留。
func unescapeCell(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) && strings.IndexByte(escapableChars, s[i+1]) >= 0 {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
