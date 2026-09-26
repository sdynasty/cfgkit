package main

import (
	"fmt"
	"strings"
)

// Kind 类型种类
type Kind int

const (
	KInt Kind = iota
	KInt64
	KFloat
	KBool
	KString
	KEnum
	KRef
	KStruct
	KList
	KMap
)

// TypeExpr 类型表达式（支持嵌套: list<struct<Drop>> / map<string,int> 等）
type TypeExpr struct {
	Kind   Kind
	Enum   string    // enum<X> 的 X
	Ref    string    // ref<X> 的 X（目标 sheet 名）
	Struct string    // struct<X> 的 X（Struct sheet 定义名）
	Elem   *TypeExpr // list<T> 的 T
	Key    *TypeExpr // map<K,V> 的 K
	Val    *TypeExpr // map<K,V> 的 V
	Uniq   bool      // 类型行后缀 #uniq：唯一索引
	Index  bool      // 类型行后缀 #index：普通索引
}

// ParseType 解析表头第2行的类型声明，如 "list<ref<Item>>"、"string#uniq"
func ParseType(s string) (*TypeExpr, error) {
	t := strings.TrimSpace(s)
	if strings.Contains(t, "?") {
		return nil, fmt.Errorf("类型 %q 含可选标记 ?: 仅结构体(Struct)定义表的字段支持 type? 与 type?=默认值，数据表列不支持", s)
	}
	uniq, index := false, false
	for changed := true; changed; {
		changed = false
		if rest, ok := trimSuffixFlag(t, "#uniq"); ok {
			uniq, t, changed = true, rest, true
		}
		if rest, ok := trimSuffixFlag(t, "#index"); ok {
			index, t, changed = true, rest, true
		}
	}
	te, err := parseTypeExpr(t)
	if err != nil {
		return nil, err
	}
	te.Uniq, te.Index = uniq, index
	if uniq && index {
		return nil, fmt.Errorf("#uniq 与 #index 不能同时使用")
	}
	if (uniq || index) && (te.Kind == KList || te.Kind == KMap || te.Kind == KStruct) {
		return nil, fmt.Errorf("list/map/struct 类型暂不支持索引")
	}
	return te, nil
}

func trimSuffixFlag(s, flag string) (string, bool) {
	if rest, ok := strings.CutSuffix(s, flag); ok {
		return strings.TrimSpace(rest), true
	}
	return s, false
}

func parseTypeExpr(s string) (*TypeExpr, error) {
	switch s {
	case "int":
		return &TypeExpr{Kind: KInt}, nil
	case "int64":
		return &TypeExpr{Kind: KInt64}, nil
	case "float":
		return &TypeExpr{Kind: KFloat}, nil
	case "bool":
		return &TypeExpr{Kind: KBool}, nil
	case "string":
		return &TypeExpr{Kind: KString}, nil
	}
	if inner, ok := unwrap(s, "enum<"); ok {
		if !isIdent(inner) {
			return nil, fmt.Errorf("非法枚举名 %q", inner)
		}
		return &TypeExpr{Kind: KEnum, Enum: inner}, nil
	}
	if inner, ok := unwrap(s, "ref<"); ok {
		if !isIdent(inner) {
			return nil, fmt.Errorf("非法引用表名 %q", inner)
		}
		return &TypeExpr{Kind: KRef, Ref: inner}, nil
	}
	if inner, ok := unwrap(s, "struct<"); ok {
		if !isIdent(inner) {
			return nil, fmt.Errorf("非法结构体名 %q", inner)
		}
		return &TypeExpr{Kind: KStruct, Struct: inner}, nil
	}
	if inner, ok := unwrap(s, "list<"); ok {
		elem, err := parseTypeExpr(inner)
		if err != nil {
			return nil, err
		}
		if elem.Kind == KList || elem.Kind == KMap {
			return nil, fmt.Errorf("暂不支持嵌套容器: %s", s)
		}
		return &TypeExpr{Kind: KList, Elem: elem}, nil
	}
	if inner, ok := unwrap(s, "map<"); ok {
		parts := strings.Split(inner, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("map<K,V> 需要恰好两个类型参数: %s", s)
		}
		k, err := parseTypeExpr(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, err
		}
		v, err := parseTypeExpr(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		if k.Kind != KString && k.Kind != KInt && k.Kind != KInt64 {
			return nil, fmt.Errorf("map 的键仅支持 string/int/int64: %s", s)
		}
		if v.Kind == KList || v.Kind == KMap {
			return nil, fmt.Errorf("暂不支持嵌套容器: %s", s)
		}
		return &TypeExpr{Kind: KMap, Key: k, Val: v}, nil
	}
	return nil, fmt.Errorf("无法识别的类型 %q（支持: int int64 float bool string enum<X> ref<X> list<T> map<K,V>）", s)
}

func unwrap(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) && strings.HasSuffix(s, ">") && len(s) > len(prefix)+1 {
		return s[len(prefix) : len(s)-1], true
	}
	return "", false
}

// GoType 返回对应的 Go 类型名。refPK 用于解析 ref<X> 目标表主键的 Go 类型。
func (t *TypeExpr) GoType(refPK func(sheet string) string) string {
	switch t.Kind {
	case KInt:
		return "int32"
	case KInt64:
		return "int64"
	case KFloat:
		return "float64"
	case KBool:
		return "bool"
	case KString:
		return "string"
	case KEnum:
		return t.Enum
	case KRef:
		return refPK(t.Ref)
	case KStruct:
		return t.Struct
	case KList:
		return "[]" + t.Elem.GoType(refPK)
	case KMap:
		return fmt.Sprintf("map[%s]%s", t.Key.GoType(refPK), t.Val.GoType(refPK))
	}
	return "any"
}
