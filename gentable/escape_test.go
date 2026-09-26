package main

import (
	"reflect"
	"strings"
	"testing"
)

// ------------------------------------------------------------ C11: 转义机制

func TestSplitUnescaped(t *testing.T) {
	tests := []struct {
		in   string
		sep  byte
		want []string
	}{
		{"a|b|c", '|', []string{"a", "b", "c"}},
		{`a\|b|c`, '|', []string{`a\|b`, "c"}},   // 转义的 | 不切
		{`a\\|b`, '|', []string{`a\\`, "b"}},     // \\ 是已配对转义，其后的 | 照常切
		{`a\|b\|c`, '|', []string{`a\|b\|c`}},    // 全部转义 = 单个元素
		{"a|", '|', []string{"a", ""}},           // 末尾空元素保留（与 strings.Split 一致）
		{"", '|', []string{""}},                  // 空串（调用方已先行特判）
		{`k:v;a:b`, ';', []string{"k:v", "a:b"}}, // struct 语境
		{`k:a\;b;c:d`, ';', []string{`k:a\;b`, "c:d"}},
	}
	for _, tt := range tests {
		got := splitUnescaped(tt.in, tt.sep)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitUnescaped(%q, %c) = %#v, 期望 %#v", tt.in, tt.sep, got, tt.want)
		}
	}
}

func TestSplitKVUnescaped(t *testing.T) {
	k, v, ok := splitKVUnescaped(`note:a\:b:c`, ':')
	if !ok || k != "note" || v != `a\:b:c` {
		t.Errorf("splitKVUnescaped = %q, %q, %v", k, v, ok)
	}
	if _, _, ok := splitKVUnescaped(`a\:b`, ':'); ok {
		t.Error("全部转义时不应切开")
	}
}

func TestUnescapeCell(t *testing.T) {
	tests := map[string]string{
		`a\|b`: "a|b",
		`a\;b`: "a;b",
		`a\:b`: "a:b",
		`a\\b`: `a\b`,
		`a\nb`: `a\nb`, // 未列出的 \x 序列原样保留（宽容策略）
		`a\`:   `a\`,   // 末尾孤立反斜杠原样保留
		"普通文本": "普通文本",
		"":     "",
	}
	for in, want := range tests {
		if got := unescapeCell(in); got != want {
			t.Errorf("unescapeCell(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestParseValueEscaped 转义在 list/struct/map 解析中生效；不含反斜杠的旧写法行为不变
func TestParseValueEscaped(t *testing.T) {
	noteF := &Field{Name: "note", Type: &TypeExpr{Kind: KString}}
	ctx := testContext()
	ctx.structs["Note"] = &StructDef{
		Name:   "Note",
		Fields: []*Field{noteF},
		byName: map[string]*Field{"note": noteF},
	}
	tests := []struct {
		typ  string
		raw  string
		want any
	}{
		{"list<string>", `a\|b|c`, []any{"a|b", "c"}},
		{"list<string>", "a|b", []any{"a", "b"}}, // 无转义，旧行为
		{"string", `C:\new`, `C:\new`},           // 未列出的 \x 原样保留
		{"struct<Note>", `note:a\;b\:c`, map[string]any{"note": "a;b:c"}},
		{"map<string,string>", `k1:a\:b;k2:c\;d`, map[string]any{"k1": "a:b", "k2": "c;d"}},
		{"map<string,int>", "a:1;b:2", map[string]any{"a": int64(1), "b": int64(2)}}, // 旧行为
		{"struct<Pos>", "x:1;y:2", map[string]any{"x": int64(1), "y": int64(2)}},     // 旧行为
	}
	for _, tt := range tests {
		t.Run(tt.typ+"="+tt.raw, func(t *testing.T) {
			got, err := parseValue(tt.raw, mustType(t, tt.typ), ctx)
			if err != nil {
				t.Fatalf("parseValue 报错: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseValue = %#v, 期望 %#v", got, tt.want)
			}
		})
	}
}

// ------------------------------------------------------------ C10: struct 可选字段与默认值

// testContextOptional 构造带可选字段的结构体: Cfg{a int 必填, b int?=5, c string?}
func testContextOptional() *Context {
	aF := &Field{Name: "a", Type: &TypeExpr{Kind: KInt}}
	bF := &Field{Name: "b", Type: &TypeExpr{Kind: KInt}, Optional: true, HasDefault: true, Default: "5"}
	cF := &Field{Name: "c", Type: &TypeExpr{Kind: KString}, Optional: true}
	ctx := testContext()
	ctx.structs["Cfg"] = &StructDef{
		Name:   "Cfg",
		Fields: []*Field{aF, bF, cF},
		byName: map[string]*Field{"a": aF, "b": bF, "c": cF},
	}
	return ctx
}

func TestStructOptionalFields(t *testing.T) {
	ctx := testContextOptional()
	typ := mustType(t, "struct<Cfg>")

	// 省略可选字段: b 物化默认值 5，c 物化零值 ""
	got, err := parseValue("a:1", typ, ctx)
	if err != nil {
		t.Fatalf("parseValue 报错: %v", err)
	}
	want := map[string]any{"a": int64(1), "b": int64(5), "c": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("省略可选字段 = %#v, 期望 %#v", got, want)
	}

	// 显式填写覆盖默认值
	got, err = parseValue("a:1;b:2;c:x", typ, ctx)
	if err != nil {
		t.Fatalf("parseValue 报错: %v", err)
	}
	want = map[string]any{"a": int64(1), "b": int64(2), "c": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("显式填写 = %#v, 期望 %#v", got, want)
	}

	// 必填字段仍强制
	_, err = parseValue("b:2", typ, ctx)
	if err == nil || !strings.Contains(err.Error(), `缺少字段 "a"`) {
		t.Errorf("缺少必填字段应报错，实际: %v", err)
	}

	// 整格留空仍是 null（Go 零值），不做物化
	got, err = parseValue("", typ, ctx)
	if err != nil || got != nil {
		t.Errorf("整格留空 = %#v, %v, 期望 nil", got, err)
	}
}

// TestStructDefaultValidation 默认值在导表期（ResolveAll）按类型解析，非法即报错
func TestStructDefaultValidation(t *testing.T) {
	tests := []struct {
		name    string
		defRows [][]any // StructCfg 定义行
		wantErr string
	}{
		{
			name: "int默认值非法",
			defRows: [][]any{
				{"name", "type", "comment"},
				{"a", "int", ""},
				{"b", "int?=abc", ""},
			},
			wantErr: `默认值 "abc" 非法`,
		},
		{
			name: "枚举默认值非法",
			defRows: [][]any{
				{"name", "type", "comment"},
				{"a", "int", ""},
				{"q", "enum<Quality>?=Rainbow", ""},
			},
			wantErr: "不是合法的 Quality 枚举值",
		},
		{
			name: "默认值为空",
			defRows: [][]any{
				{"name", "type", "comment"},
				{"a", "int?=", ""},
			},
			wantErr: "默认值不能为空",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := buildDir(t, []wbSpec{
				commonWB(sheetSpec{"StructCfg", tt.defRows}),
			})
			errs := collectErrs(dir)
			joined := ""
			for _, e := range errs {
				joined += e.Error() + "\n"
			}
			if !strings.Contains(joined, tt.wantErr) {
				t.Errorf("错误信息应包含 %q，实际:\n%s", tt.wantErr, joined)
			}
		})
	}
}

// TestStructOptionalExcel excel 级: 可选字段省略时 JSON 物化默认值
func TestStructOptionalExcel(t *testing.T) {
	dir := buildDir(t, []wbSpec{
		commonWB(sheetSpec{"StructCfg", [][]any{
			{"name", "type", "comment"},
			{"a", "int", "必填"},
			{"b", "int?=5", "可选带默认值"},
			{"q", "enum<Quality>?=Green", "可选枚举默认值"},
			{"tags", "list<int>?", "可选零值"},
		}}),
		{"cfg.xlsx", []sheetSpec{{"ItemCfg", [][]any{
			{"id", "cfg"},
			{"int", "struct<Cfg>"},
			{"编号", "配置"},
			{"cs", "cs"},
			{1, "a:1"},
		}}}},
	})
	tables, enums, structs, _, errs := LoadExcels(dir)
	if len(errs) != 0 {
		t.Fatalf("LoadExcels 报错: %v", errs)
	}
	if errs = ResolveAll(tables, enums, structs); len(errs) != 0 {
		t.Fatalf("ResolveAll 报错: %v", errs)
	}
	tb := findTable(t, tables, "ItemCfg")
	want := map[string]any{"a": int64(1), "b": int64(5), "q": "Green", "tags": []any{}}
	if !reflect.DeepEqual(tb.Values[0]["cfg"], want) {
		t.Errorf("cfg = %#v, 期望 %#v", tb.Values[0]["cfg"], want)
	}
}
