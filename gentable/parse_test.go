package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// ------------------------------------------------------------ 基础值解析

func TestParseInt(t *testing.T) {
	ok := map[string]int64{
		"100":       100,
		"-5":        -5,
		"1001.0":    1001, // Excel 把整数显示成小数
		"1.001E+03": 1001, // 科学计数法
		"0":         0,
	}
	for in, want := range ok {
		got, err := parseInt(in)
		if err != nil {
			t.Errorf("parseInt(%q) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseInt(%q) = %d, 期望 %d", in, got, want)
		}
	}
	for _, in := range []string{"", "abc", "3.5", "1e999"} {
		if _, err := parseInt(in); err == nil {
			t.Errorf("parseInt(%q) 应当报错", in)
		}
	}
}

// testContext 构造覆盖 enum/ref/struct 的解析上下文
func testContext() *Context {
	enum := &EnumDef{
		Name:        "Quality",
		valueByName: map[string]int64{"White": 1, "Green": 2},
		nameByValue: map[int64]string{1: "White", 2: "Green"},
		Entries:     []EnumEntry{{Name: "White", Value: 1}, {Name: "Green", Value: 2}},
	}
	xF := &Field{Name: "x", Type: &TypeExpr{Kind: KInt}}
	yF := &Field{Name: "y", Type: &TypeExpr{Kind: KInt}}
	strct := &StructDef{
		Name:   "Pos",
		Fields: []*Field{xF, yF},
		byName: map[string]*Field{"x": xF, "y": yF},
	}
	item := &Table{
		Sheet:  "Item",
		PK:     &Field{Name: "id", Type: &TypeExpr{Kind: KInt}},
		pkSeen: map[any]int{int64(1001): 6, int64(1002): 7},
	}
	return &Context{
		enums:   map[string]*EnumDef{"Quality": enum},
		structs: map[string]*StructDef{"Pos": strct},
		tables:  map[string]*Table{"Item": item},
	}
}

func mustType(t *testing.T, s string) *TypeExpr {
	t.Helper()
	te, err := ParseType(s)
	if err != nil {
		t.Fatalf("ParseType(%q): %v", s, err)
	}
	return te
}

func TestParseValueOK(t *testing.T) {
	ctx := testContext()
	tests := []struct {
		typ  string
		raw  string
		want any
	}{
		{"int", "42", int64(42)},
		{"int", "", int64(0)},
		{"float", "3.5", 3.5},
		{"float", "", float64(0)},
		{"bool", "1", true},
		{"bool", "是", true},
		{"bool", "true", true},
		{"bool", "0", false},
		{"bool", "否", false},
		{"bool", "", false},
		{"string", "文本", "文本"}, // 上游读格时已 TrimSpace，parseValue 原样返回
		{"enum<Quality>", "White", "White"},
		{"ref<Item>", "1001", int64(1001)},
		{"ref<Item>", "", int64(0)},
		{"list<int>", "1|2|3", []any{int64(1), int64(2), int64(3)}},
		{"list<int>", "", []any{}},
		{"list<ref<Item>>", "1001|1002", []any{int64(1001), int64(1002)}},
		{"map<string,int>", "a:1;b:2", map[string]any{"a": int64(1), "b": int64(2)}},
		{"map<string,int>", "", map[string]any{}},
		{"struct<Pos>", "x:1;y:2", map[string]any{"x": int64(1), "y": int64(2)}},
		{"struct<Pos>", "y:2;x:1", map[string]any{"x": int64(1), "y": int64(2)}}, // 顺序无关
		{"struct<Pos>", "", nil}, // 整格留空 = null = Go 零值
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

func TestParseValueErrors(t *testing.T) {
	ctx := testContext()
	tests := []struct {
		typ     string
		raw     string
		wantErr string // 错误信息应包含的子串
	}{
		{"int", "abc", "无法解析为整数"},
		{"bool", "maybe", "bool 仅接受"},
		{"enum<Quality>", "Rainbow", "不是合法的 Quality 枚举值"},
		{"enum<Quality>", "", "不能为空"},
		{"enum<NotExist>", "X", "未定义"},
		{"ref<Item>", "9999", "引用了不存在的 Item 主键"},
		{"ref<NoTable>", "1", "不存在"},
		{"list<ref<Item>>", "1001|9999", "引用了不存在的 Item 主键"},
		{"map<string,int>", "a1", "缺少 ':'"},
		{"map<string,int>", "a:x", "无法解析为整数"},
		{"struct<Pos>", "x:1", `缺少字段 "y"`},
		{"struct<Pos>", "x:1;y:2;z:3", "不是 Pos 的字段"},
		{"struct<Pos>", "x:1;x:2;y:3", "重复填写"},
		{"struct<Pos>", "x:1;y", "缺少 ':'"},
	}
	for _, tt := range tests {
		t.Run(tt.typ+"="+tt.raw, func(t *testing.T) {
			_, err := parseValue(tt.raw, mustType(t, tt.typ), ctx)
			if err == nil {
				t.Fatalf("parseValue(%q) 应当报错", tt.raw)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("错误信息 %q 应包含 %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// ------------------------------------------------------------ Excel 集成测试

func setRows(t *testing.T, f *excelize.File, sheet string, rows [][]any) {
	t.Helper()
	found := false
	for _, s := range f.GetSheetList() {
		if s == sheet {
			found = true
			break
		}
	}
	if !found {
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatal(err)
		}
	}
}

type sheetSpec struct {
	name string
	rows [][]any
}
type wbSpec struct {
	file   string
	sheets []sheetSpec
}

func buildDir(t *testing.T, wbs []wbSpec) string {
	t.Helper()
	dir := t.TempDir()
	for _, wb := range wbs {
		f := excelize.NewFile()
		for i, sh := range wb.sheets {
			if i == 0 {
				if err := f.SetSheetName("Sheet1", sh.name); err != nil {
					t.Fatal(err)
				}
			}
			setRows(t, f, sh.name, sh.rows)
		}
		if err := f.SaveAs(filepath.Join(dir, wb.file)); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	return dir
}

var (
	enumQualityRows = [][]any{{"name", "value", "comment"}, {"White", 1, "白"}, {"Green", 2, "绿"}}
	structPosRows   = [][]any{{"name", "type", "comment"}, {"x", "int", "X"}, {"y", "int", "Y"}}
	itemHeader      = [][]any{
		{"id", "name", "quality"},
		{"int", "string#uniq", "enum<Quality>"},
		{"编号", "名字", "品质"},
		{"cs", "cs", "cs"},
	}
	monsterHeader = [][]any{
		{"id", "pos", "drops"},
		{"int", "struct<Pos>", "list<ref<Item>>"},
		{"编号", "坐标", "掉落"},
		{"cs", "cs", "s"},
	}
)

func commonWB(extra ...sheetSpec) wbSpec {
	sheets := []sheetSpec{{"EnumQuality", enumQualityRows}, {"StructPos", structPosRows}}
	return wbSpec{"common.xlsx", append(sheets, extra...)}
}

func itemWB(data ...[]any) wbSpec {
	rows := make([][]any, 0, len(itemHeader)+len(data))
	rows = append(rows, itemHeader...)
	rows = append(rows, data...)
	return wbSpec{"item.xlsx", []sheetSpec{{"Item", rows}}}
}

func monsterWB(data ...[]any) wbSpec {
	rows := make([][]any, 0, len(monsterHeader)+len(data))
	rows = append(rows, monsterHeader...)
	rows = append(rows, data...)
	return wbSpec{"monster.xlsx", []sheetSpec{{"Monster", rows}}}
}

func collectErrs(dir string) []error {
	tables, enums, structs, errs := LoadExcels(dir)
	if len(errs) == 0 {
		errs = append(errs, ResolveAll(tables, enums, structs)...)
	}
	return errs
}

func findTable(t *testing.T, tables []*Table, sheet string) *Table {
	t.Helper()
	for _, tb := range tables {
		if tb.Sheet == sheet {
			return tb
		}
	}
	t.Fatalf("未找到表 %s", sheet)
	return nil
}

func TestLoadAndResolveValid(t *testing.T) {
	dir := buildDir(t, []wbSpec{
		commonWB(),
		itemWB(
			[]any{"# ===== 武器 ====="}, // 注释行应被跳过
			[]any{1001, "木剑", "White"},
			[]any{1002, "铁剑", "Green"},
		),
		monsterWB(
			[]any{2001, "x:1;y:2", "1001|1002"},
		),
	})
	// 锁文件/临时文件应被跳过
	os.WriteFile(filepath.Join(dir, ".~lock.xlsx"), []byte("junk"), 0o644)
	os.WriteFile(filepath.Join(dir, "~$temp.xlsx"), []byte("junk"), 0o644)

	tables, enums, structs, errs := LoadExcels(dir)
	if len(errs) != 0 {
		t.Fatalf("LoadExcels 报错: %v", errs)
	}
	if len(tables) != 2 || len(enums) != 1 || len(structs) != 1 {
		t.Fatalf("数量不符: tables=%d enums=%d structs=%d", len(tables), len(enums), len(structs))
	}
	if errs = ResolveAll(tables, enums, structs); len(errs) != 0 {
		t.Fatalf("ResolveAll 报错: %v", errs)
	}

	item := findTable(t, tables, "Item")
	if len(item.Values) != 2 {
		t.Fatalf("Item 应有 2 行（注释行跳过），实际 %d", len(item.Values))
	}
	if item.Values[0]["id"] != int64(1001) || item.Values[0]["quality"] != "White" {
		t.Errorf("Item 第1行解析错误: %#v", item.Values[0])
	}
	monster := findTable(t, tables, "Monster")
	wantPos := map[string]any{"x": int64(1), "y": int64(2)}
	if !reflect.DeepEqual(monster.Values[0]["pos"], wantPos) {
		t.Errorf("Monster.pos = %#v, 期望 %#v", monster.Values[0]["pos"], wantPos)
	}
	wantDrops := []any{int64(1001), int64(1002)}
	if !reflect.DeepEqual(monster.Values[0]["drops"], wantDrops) {
		t.Errorf("Monster.drops = %#v, 期望 %#v", monster.Values[0]["drops"], wantDrops)
	}
	if enums[0].Name != "Quality" || structs[0].Name != "Pos" {
		t.Errorf("枚举/结构体命名错误: %s / %s", enums[0].Name, structs[0].Name)
	}
}

func TestLoadAndResolveErrors(t *testing.T) {
	tests := []struct {
		name    string
		wbs     []wbSpec
		wantErr string
	}{
		{
			name: "主键重复",
			wbs: []wbSpec{commonWB(), itemWB(
				[]any{1001, "木剑", "White"},
				[]any{1001, "铁剑", "Green"},
			)},
			wantErr: "主键 1001 与第",
		},
		{
			name: "主键导出标记非cs",
			wbs: []wbSpec{{"item.xlsx", []sheetSpec{{"Item", [][]any{
				{"id", "name"},
				{"int", "string"},
				{"编号", "名字"},
				{"s", "cs"}, // 主键标记 s
				{1001, "木剑"},
			}}}}},
			wantErr: "主键列两端都要用",
		},
		{
			name: "非法枚举值",
			wbs: []wbSpec{commonWB(), itemWB(
				[]any{1001, "木剑", "Rainbow"},
			)},
			wantErr: "不是合法的 Quality 枚举值",
		},
		{
			name: "ref 引用不存在",
			wbs: []wbSpec{commonWB(),
				itemWB([]any{1001, "木剑", "White"}),
				monsterWB([]any{2001, "x:1;y:2", "9999"}),
			},
			wantErr: "引用了不存在的 Item 主键: 9999",
		},
		{
			name: "struct 缺字段",
			wbs: []wbSpec{commonWB(),
				itemWB([]any{1001, "木剑", "White"}),
				monsterWB([]any{2001, "x:1", "1001"}),
			},
			wantErr: `缺少字段 "y"`,
		},
		{
			name: "唯一索引重复",
			wbs: []wbSpec{commonWB(), itemWB(
				[]any{1001, "木剑", "White"},
				[]any{1002, "木剑", "Green"},
			)},
			wantErr: "唯一索引值 木剑 与第",
		},
		{
			name: "结构体循环嵌套",
			wbs: []wbSpec{commonWB(
				sheetSpec{"StructA", [][]any{{"name", "type", "comment"}, {"b", "struct<B>", ""}}},
				sheetSpec{"StructB", [][]any{{"name", "type", "comment"}, {"a", "struct<A>", ""}}},
			)},
			wantErr: "结构体循环嵌套: A -> B -> A",
		},
		{
			name: "枚举未定义",
			wbs: []wbSpec{{"item.xlsx", []sheetSpec{{"Item", [][]any{
				{"id", "q"},
				{"int", "enum<NotExist>"},
				{"编号", "品质"},
				{"cs", "cs"},
				{1, "White"},
			}}}}},
			wantErr: "引用了未定义的枚举 NotExist",
		},
		{
			name: "表头不足4行",
			wbs: []wbSpec{{"bad.xlsx", []sheetSpec{{"Bad", [][]any{
				{"id", "name"},
				{"int", "string"},
			}}}}},
			wantErr: "表头不足4行",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := collectErrs(buildDir(t, tt.wbs))
			if len(errs) == 0 {
				t.Fatalf("应当报错 %q，但没有错误", tt.wantErr)
			}
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
