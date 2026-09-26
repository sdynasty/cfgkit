package main

import (
	"fmt"
	"strings"
	"unicode"
)

// isIdent 判断是否为合法标识符: 字母/下划线开头，后接字母/数字/下划线
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}

// upperCamel 字段名转 Go 导出名: id -> Id, stack_max -> StackMax
func upperCamel(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		if r == '_' {
			upper = true
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lowerFirst 首字母小写: ItemQuality -> itemQuality
func lowerFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToLower(r)) + s[i+1:]
	}
	return s
}

// toSnake 生成 JSON 文件名: Item -> item, MonsterDrop -> monster_drop
func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// goKeywords Go 关键字（sheet/枚举/结构体名用作 Go 类型名，不能与之冲突）
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// validGoTypeName 校验用作 Go 类型名的名字（数据表 sheet 名、枚举名、结构体名）。
// 这些名字直接拼进生成代码，不合法会在编译期才炸——提前到导表期拦截。
func validGoTypeName(name string) error {
	if name == "" {
		return fmt.Errorf("名字为空")
	}
	if !isIdent(name) {
		return fmt.Errorf("%q 不是合法 Go 标识符（仅字母/数字/下划线，且不以数字开头）", name)
	}
	if goKeywords[name] {
		return fmt.Errorf("%q 是 Go 关键字", name)
	}
	return nil
}
