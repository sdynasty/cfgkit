package main

import "testing"

func TestParseType(t *testing.T) {
	tests := []struct {
		in    string
		kind  Kind
		enum  string
		ref   string
		strct string
		elem  Kind // KList 的元素；-1 表示不检查
		key   Kind // KMap 的键；-1 表示不检查
		val   Kind // KMap 的值；-1 表示不检查
		uniq  bool
		index bool
	}{
		{in: "int", kind: KInt, elem: -1, key: -1, val: -1},
		{in: "  int64  ", kind: KInt64, elem: -1, key: -1, val: -1},
		{in: "float", kind: KFloat, elem: -1, key: -1, val: -1},
		{in: "bool", kind: KBool, elem: -1, key: -1, val: -1},
		{in: "string", kind: KString, elem: -1, key: -1, val: -1},
		{in: "enum<ItemQuality>", kind: KEnum, enum: "ItemQuality", elem: -1, key: -1, val: -1},
		{in: "ref<Item>", kind: KRef, ref: "Item", elem: -1, key: -1, val: -1},
		{in: "struct<Drop>", kind: KStruct, strct: "Drop", elem: -1, key: -1, val: -1},
		{in: "list<int>", kind: KList, elem: KInt, key: -1, val: -1},
		{in: "list<ref<Item>>", kind: KList, elem: KRef, key: -1, val: -1},
		{in: "list<struct<Drop>>", kind: KList, elem: KStruct, key: -1, val: -1},
		{in: "map<string,int>", kind: KMap, elem: -1, key: KString, val: KInt},
		{in: "map<int,string>", kind: KMap, elem: -1, key: KInt, val: KString},
		{in: "map<string,struct<Pos>>", kind: KMap, elem: -1, key: KString, val: KStruct},
		{in: "string#uniq", kind: KString, elem: -1, key: -1, val: -1, uniq: true},
		{in: "string#index", kind: KString, elem: -1, key: -1, val: -1, index: true},
		{in: "int #uniq", kind: KInt, elem: -1, key: -1, val: -1, uniq: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			te, err := ParseType(tt.in)
			if err != nil {
				t.Fatalf("ParseType(%q) 报错: %v", tt.in, err)
			}
			if te.Kind != tt.kind {
				t.Errorf("Kind = %v, 期望 %v", te.Kind, tt.kind)
			}
			if te.Enum != tt.enum || te.Ref != tt.ref || te.Struct != tt.strct {
				t.Errorf("参数名不符: enum=%q ref=%q struct=%q", te.Enum, te.Ref, te.Struct)
			}
			if tt.elem >= 0 {
				if te.Elem == nil || te.Elem.Kind != tt.elem {
					t.Errorf("Elem = %v, 期望 %v", te.Elem, tt.elem)
				}
			}
			if tt.key >= 0 {
				if te.Key == nil || te.Key.Kind != tt.key || te.Val == nil || te.Val.Kind != tt.val {
					t.Errorf("Key/Val = %v/%v, 期望 %v/%v", te.Key, te.Val, tt.key, tt.val)
				}
			}
			if te.Uniq != tt.uniq || te.Index != tt.index {
				t.Errorf("索引标记 uniq=%v index=%v, 期望 uniq=%v index=%v", te.Uniq, te.Index, tt.uniq, tt.index)
			}
		})
	}
}

func TestParseTypeErrors(t *testing.T) {
	bad := []string{
		"uint",                  // 不支持的类型
		"enum<>",                // 空枚举名
		"ref<Item",              // 未闭合
		"list<list<int>>",       // 嵌套容器
		"map<string,list<int>>", // map 值为容器
		"map<bool,int>",         // 非法键类型
		"map<string>",           // 缺值类型
		"string#uniq#index",     // 互斥标记
		"list<int>#index",       // 容器不支持索引
		"struct<Pos>#uniq",      // 结构体不支持索引
		"Enum<Item>",            // 大小写敏感
	}
	for _, in := range bad {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseType(in); err == nil {
				t.Errorf("ParseType(%q) 应当报错", in)
			}
		})
	}
}

func TestGoType(t *testing.T) {
	refPK := func(sheet string) string {
		if sheet == "StrTable" {
			return "string"
		}
		return "int32"
	}
	tests := []struct {
		in   string
		want string
	}{
		{"int", "int32"},
		{"int64", "int64"},
		{"float", "float64"},
		{"bool", "bool"},
		{"string", "string"},
		{"enum<ItemQuality>", "ItemQuality"},
		{"struct<Drop>", "Drop"},
		{"ref<Item>", "int32"},
		{"ref<StrTable>", "string"},
		{"list<ref<Item>>", "[]int32"},
		{"list<struct<Drop>>", "[]Drop"},
		{"map<string,int>", "map[string]int32"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			te, err := ParseType(tt.in)
			if err != nil {
				t.Fatalf("ParseType(%q): %v", tt.in, err)
			}
			if got := te.GoType(refPK); got != tt.want {
				t.Errorf("GoType = %q, 期望 %q", got, tt.want)
			}
		})
	}
}
