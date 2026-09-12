package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkField(name, flag string, kind Kind) *Field {
	return &Field{Name: name, GoName: upperCamel(name), Flag: flag, Type: &TypeExpr{Kind: kind}}
}

func writeBaseline(t *testing.T, dir, sheet string, rows []map[string]any) {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, toSnake(sheet)+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiffSideModified(t *testing.T) {
	dir := t.TempDir()
	writeBaseline(t, dir, "Item", []map[string]any{
		{"id": 1001, "price": 100},
		{"id": 1002, "price": 200},
	})
	tbl := &Table{
		File: "item.xlsx", Sheet: "Item",
		Fields: []*Field{mkField("id", "cs", KInt), mkField("price", "cs", KInt)},
		Values: []map[string]any{
			{"id": int64(1001), "price": int64(150)}, // 改
			{"id": int64(1003), "price": int64(300)}, // 增（1002 被删）
		},
	}
	diffs := DiffSide([]*Table{tbl}, "server", dir)
	if len(diffs) != 1 {
		t.Fatalf("应有 1 个表差异，实际 %d", len(diffs))
	}
	d := diffs[0]
	if d.IsNewTable {
		t.Error("不应判定为新表")
	}
	if len(d.AddedPKs) != 1 || d.AddedPKs[0] != "1003" {
		t.Errorf("AddedPKs = %v, 期望 [1003]", d.AddedPKs)
	}
	if len(d.RemovedPKs) != 1 || d.RemovedPKs[0] != "1002" {
		t.Errorf("RemovedPKs = %v, 期望 [1002]", d.RemovedPKs)
	}
	if len(d.Modified) != 1 || d.Modified[0].PK != "1001" {
		t.Fatalf("Modified = %v, 期望 1 条 pk=1001", d.Modified)
	}
	c := d.Modified[0].Changes[0]
	if c.Field != "price" || c.OldStr != "100" || c.NewStr != "150" {
		t.Errorf("字段变更 = %+v, 期望 price 100→150", c)
	}
}

func TestDiffSideSchemaAdded(t *testing.T) {
	dir := t.TempDir()
	writeBaseline(t, dir, "Item", []map[string]any{
		{"id": 1001, "price": 100},
	})
	tbl := &Table{
		File: "item.xlsx", Sheet: "Item",
		Fields: []*Field{mkField("id", "cs", KInt), mkField("price", "cs", KInt), mkField("vip_price", "s", KInt)},
		Values: []map[string]any{
			{"id": int64(1001), "price": int64(100), "vip_price": int64(80)},
		},
	}
	diffs := DiffSide([]*Table{tbl}, "server", dir)
	if len(diffs) != 1 {
		t.Fatalf("应有 1 个表差异，实际 %d", len(diffs))
	}
	d := diffs[0]
	if len(d.SchemaAdded) != 1 || d.SchemaAdded[0] != "vip_price" {
		t.Errorf("SchemaAdded = %v, 期望 [vip_price]", d.SchemaAdded)
	}
	// 新增列不应逐行报告为 Modified（防刷屏规则）
	if len(d.Modified) != 0 {
		t.Errorf("新增列不应产生行级 Modified，实际 %v", d.Modified)
	}
}

func TestDiffSideNewTableAndNoChange(t *testing.T) {
	dir := t.TempDir() // 空目录 = 无基线
	tbl := &Table{
		File: "item.xlsx", Sheet: "Item",
		Fields: []*Field{mkField("id", "cs", KInt)},
		Values: []map[string]any{{"id": int64(1)}, {"id": int64(2)}},
	}
	diffs := DiffSide([]*Table{tbl}, "client", dir)
	if len(diffs) != 1 || !diffs[0].IsNewTable || len(diffs[0].AddedPKs) != 2 {
		t.Fatalf("应判定为新表且 2 行，实际 %#v", diffs)
	}

	// 写回基线后应无差异
	writeBaseline(t, dir, "Item", []map[string]any{{"id": 1}, {"id": 2}})
	if diffs = DiffSide([]*Table{tbl}, "client", dir); len(diffs) != 0 {
		t.Fatalf("应无差异，实际 %#v", diffs)
	}
}

func TestPrettyVal(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{float64(100), "100"},
		{float64(0.5), "0.5"},
		{int64(7), "7"},
		{"文本", "文本"},
		{true, "true"},
		{nil, "<空>"},
		{[]any{int64(1), int64(2)}, "[1,2]"},
	}
	for _, tt := range tests {
		if got := prettyVal(tt.in); got != tt.want {
			t.Errorf("prettyVal(%#v) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildDiffReport(t *testing.T) {
	diffs := map[string][]TableDiff{
		"server": {{
			Sheet: "Item", File: "item.xlsx",
			Modified: []RowMod{{PK: "1001", Changes: []FieldChange{{Field: "price", OldStr: "100", NewStr: "150"}}}},
		}},
	}
	report := BuildDiffReport(diffs, []string{"server/enum_gen.go", "server/tables_gen.go"}, false)
	for _, want := range []string{"[server]", "Item(item.xlsx)", "price: 100 → 150", "定义变更", "enum_gen.go"} {
		if !strings.Contains(report, want) {
			t.Errorf("报告应包含 %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "tables_gen.go") {
		t.Errorf("tables_gen.go 不应出现在定义变更里:\n%s", report)
	}

	first := BuildDiffReport(nil, nil, true)
	if !strings.Contains(first, "首次导出") {
		t.Errorf("首次导出提示缺失:\n%s", first)
	}
	same := BuildDiffReport(map[string][]TableDiff{}, nil, false)
	if !strings.Contains(same, "数据无变化") {
		t.Errorf("无变化提示缺失:\n%s", same)
	}
}
