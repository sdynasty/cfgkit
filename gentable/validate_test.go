package main

import (
	"strings"
	"testing"
)

// ------------------------------------------------------------ 导出期校验（A 组）

// TestValidationErrors 覆盖: int/枚举值 int32 范围、sheet 名、GoName 碰撞、
// JSON 文件名碰撞、首列不导出时主键定位、Enum/Struct 前缀误判
func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		wbs     []wbSpec
		wantErr string
	}{
		{
			name: "int字段超出int32",
			wbs: []wbSpec{commonWB(), {"num.xlsx", []sheetSpec{{"Num", [][]any{
				{"id", "count"},
				{"int", "int"},
				{"编号", "数量"},
				{"cs", "cs"},
				{1, 2147483648},
			}}}}},
			wantErr: "超出 int32 范围",
		},
		{
			name: "int主键超出int32",
			wbs: []wbSpec{commonWB(), itemWB(
				[]any{3000000000, "木剑", "White"},
			)},
			wantErr: "主键值 3000000000 超出 int32 范围",
		},
		{
			name: "int字段负向越界",
			wbs: []wbSpec{commonWB(), {"num.xlsx", []sheetSpec{{"Num", [][]any{
				{"id", "count"},
				{"int", "int"},
				{"编号", "数量"},
				{"cs", "cs"},
				{1, -2147483649},
			}}}}},
			wantErr: "超出 int32 范围",
		},
		{
			name: "枚举值超出int32",
			wbs: []wbSpec{{"common.xlsx", []sheetSpec{{"EnumBig", [][]any{
				{"name", "value", "comment"},
				{"Huge", "2147483648", ""},
			}}}}},
			wantErr: "超出 int32 范围",
		},
		{
			name: "sheet名非法Go标识符",
			wbs: []wbSpec{{"bad.xlsx", []sheetSpec{{"1Item", [][]any{
				{"id"},
				{"int"},
				{"编号"},
				{"cs"},
				{1},
			}}}}},
			wantErr: `sheet 名"1Item" 不是合法 Go 标识符`,
		},
		{
			name: "sheet名是Go关键字",
			wbs: []wbSpec{{"bad.xlsx", []sheetSpec{{"type", [][]any{
				{"id"},
				{"int"},
				{"编号"},
				{"cs"},
				{1},
			}}}}},
			wantErr: `"type" 是 Go 关键字`,
		},
		{
			name: "字段Go名碰撞",
			wbs: []wbSpec{commonWB(), {"item.xlsx", []sheetSpec{{"Item", [][]any{
				{"id", "stack_max", "stackMax"},
				{"int", "int", "int"},
				{"编号", "堆叠上限", "堆叠上限2"},
				{"cs", "cs", "cs"},
				{1, 99, 99},
			}}}}},
			wantErr: `字段 "stack_max" 与 "stackMax" 转换后的 Go 名相同（都是 StackMax）`,
		},
		{
			name: "结构体字段Go名碰撞",
			wbs: []wbSpec{{"common.xlsx", []sheetSpec{{"StructSize", [][]any{
				{"name", "type", "comment"},
				{"max_hp", "int", ""},
				{"maxHp", "int", ""},
			}}}}},
			wantErr: `字段 "max_hp" 与 "maxHp" 转换后的 Go 名相同（都是 MaxHp）`,
		},
		{
			name: "表与结构体类型名碰撞",
			wbs: []wbSpec{
				commonWB(), // 含 StructPos
				{"pos.xlsx", []sheetSpec{{"Pos", [][]any{
					{"id"},
					{"int"},
					{"编号"},
					{"cs"},
					{1},
				}}}},
			},
			wantErr: `Go 标识符 "Pos" 冲突`,
		},
		{
			name: "表名撞框架保留名",
			wbs: []wbSpec{{"cfg.xlsx", []sheetSpec{{"Config", [][]any{
				{"id"},
				{"int"},
				{"编号"},
				{"cs"},
				{1},
			}}}}},
			wantErr: `Go 标识符 "Config" 冲突`,
		},
		{
			name: "JSON文件名碰撞",
			wbs: []wbSpec{
				{"a.xlsx", []sheetSpec{{"myTable", [][]any{
					{"id"}, {"int"}, {"编号"}, {"cs"}, {1},
				}}}},
				{"b.xlsx", []sheetSpec{{"MyTable", [][]any{
					{"id"}, {"int"}, {"编号"}, {"cs"}, {1},
				}}}},
			},
			wantErr: `JSON 文件名 "my_table.json" 冲突: 表 a.xlsx[myTable] 与 b.xlsx[MyTable]`,
		},
		{
			name: "首列不导出时主键定位",
			wbs: []wbSpec{{"item.xlsx", []sheetSpec{{"Item", [][]any{
				{"id", "name"},
				{"int", "string"},
				{"编号", "名字"},
				{"-", "cs"},
				{1001, "木剑"},
			}}}}},
			wantErr: "主键必须放在第1列",
		},
		{
			name: "Enum前缀表头大小写不符仍报精准错误",
			wbs: []wbSpec{{"common.xlsx", []sheetSpec{{"EnumBad", [][]any{
				{"name", "value", "Comment"},
				{"A", "1", ""},
			}}}}},
			wantErr: `表头第3列应为 "comment"`,
		},
		{
			name: "sheet名去掉Enum前缀为空",
			wbs: []wbSpec{{"common.xlsx", []sheetSpec{{"Enum", [][]any{
				{"name", "value", "comment"},
				{"A", "1", ""},
			}}}}},
			wantErr: "去掉 Enum 前缀后为空",
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

// TestEnumStructPrefixMisdetect Enum/Struct 前缀的数据表（表头不是定义表格式）应按数据表处理
func TestEnumStructPrefixMisdetect(t *testing.T) {
	dir := buildDir(t, []wbSpec{
		{"data.xlsx", []sheetSpec{
			{"EnumValue", [][]any{ // 名字叫 EnumValue 但表头是数据表格式
				{"id", "name"},
				{"int", "string"},
				{"编号", "名字"},
				{"cs", "cs"},
				{1, "甲"},
			}},
			{"Structure", [][]any{ // 名字叫 Structure 但表头是数据表格式
				{"id", "size"},
				{"int", "int"},
				{"编号", "尺寸"},
				{"cs", "cs"},
				{1, 10},
			}},
		}},
	})
	tables, _, _, _, errs := LoadExcels(dir)
	if len(errs) != 0 {
		t.Fatalf("EnumValue/Structure 数据表不应报错: %v", errs)
	}
	if errs = ResolveAll(tables, nil, nil); len(errs) != 0 {
		t.Fatalf("ResolveAll 报错: %v", errs)
	}
	for _, sheet := range []string{"EnumValue", "Structure"} {
		tb := findTable(t, tables, sheet)
		if len(tb.Values) != 1 {
			t.Errorf("%s 应有 1 行数据，实际 %d", sheet, len(tb.Values))
		}
	}
}
