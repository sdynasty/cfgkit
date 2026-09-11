package main

import (
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
