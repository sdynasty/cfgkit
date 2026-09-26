package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ------------------------------------------------------------ 导表 diff 摘要
//
// 在写入新产物之前，把本次解析结果与上一次导出的 JSON（基线）对比，
// 输出每个端每张表的: 新增行 / 删除行 / 字段级修改 / schema 变更（新增或删除列）。
// 另外对比生成代码文件内容，捕捉「JSON 数据没变但定义变了」的情况
// （如枚举 White 的值从 1 改成 2——行数据里存的是名字，看不出来）。

const maxLinesPerSection = 8 // 每类明细最多展示条数，超出折叠为计数

// FieldChange 一个字段的新旧值（已格式化为可读文本）
type FieldChange struct {
	Field  string
	OldStr string
	NewStr string
}

// RowMod 一行内的全部字段变更
type RowMod struct {
	PK      string
	Changes []FieldChange
}

// TableDiff 一张表在某个端的差异
type TableDiff struct {
	Sheet, File string
	IsNewTable  bool // 基线不存在（首次导出该表）
	AddedPKs    []string
	RemovedPKs  []string
	Modified    []RowMod
	SchemaAdded []string
	SchemaGone  []string
}

func (d *TableDiff) Empty() bool {
	return !d.IsNewTable && len(d.AddedPKs) == 0 && len(d.RemovedPKs) == 0 &&
		len(d.Modified) == 0 && len(d.SchemaAdded) == 0 && len(d.SchemaGone) == 0
}

// DiffSide 对比某一端全部表与基线目录下的 JSON
func DiffSide(tables []*Table, side, baseDir string) []TableDiff {
	var diffs []TableDiff
	for _, t := range tables {
		fs := fieldsFor(t, side)
		pkName := fs[0].Name
		d := TableDiff{Sheet: t.Sheet, File: t.File}

		raw, err := os.ReadFile(filepath.Join(baseDir, toSnake(t.Sheet)+".json"))
		if err != nil {
			d.IsNewTable = true
			for _, vals := range t.Values {
				d.AddedPKs = append(d.AddedPKs, pkString(vals[pkName]))
			}
			diffs = append(diffs, d)
			continue
		}
		var oldRows []map[string]any
		if err := json.Unmarshal(raw, &oldRows); err != nil {
			d.IsNewTable = true // 基线损坏，按全新处理（导表本身会重写它）
			for _, vals := range t.Values {
				d.AddedPKs = append(d.AddedPKs, pkString(vals[pkName]))
			}
			diffs = append(diffs, d)
			continue
		}

		// 旧表按主键索引 + schema 对比
		old := make(map[string]map[string]any, len(oldRows))
		oldKeys := map[string]bool{}
		for _, r := range oldRows {
			old[pkString(r[pkName])] = r
			for k := range r {
				oldKeys[k] = true
			}
		}
		newKeys := make(map[string]bool, len(fs))
		for _, f := range fs {
			newKeys[f.Name] = true
			if !oldKeys[f.Name] && len(oldRows) > 0 {
				d.SchemaAdded = append(d.SchemaAdded, f.Name)
			}
		}
		for k := range oldKeys {
			if !newKeys[k] {
				d.SchemaGone = append(d.SchemaGone, k)
			}
		}
		sort.Strings(d.SchemaAdded)
		sort.Strings(d.SchemaGone)
		schemaAddedSet := map[string]bool{}
		for _, k := range d.SchemaAdded {
			schemaAddedSet[k] = true
		}

		// 行级对比
		for _, vals := range t.Values {
			pk := pkString(vals[pkName])
			oldRow, ok := old[pk]
			if !ok {
				d.AddedPKs = append(d.AddedPKs, pk)
				continue
			}
			delete(old, pk)
			var changes []FieldChange
			for _, f := range fs {
				if schemaAddedSet[f.Name] {
					continue // 新增列的逐行变化已由 schema 变更概括，不再刷屏
				}
				nv := vals[f.Name]
				ov, existed := oldRow[f.Name]
				if !existed {
					ov = nil
				}
				if canonJSON(ov) != canonJSON(nv) {
					changes = append(changes, FieldChange{Field: f.Name, OldStr: prettyVal(ov), NewStr: prettyVal(nv)})
				}
			}
			if len(changes) > 0 {
				d.Modified = append(d.Modified, RowMod{PK: pk, Changes: changes})
			}
		}
		for pk := range old {
			d.RemovedPKs = append(d.RemovedPKs, pk)
		}
		sort.Strings(d.RemovedPKs)

		if !d.Empty() {
			diffs = append(diffs, d)
		}
	}
	return diffs
}

// SnapshotCode 记录已生成代码文件的内容，用于导表后对比定义变更
func SnapshotCode(codeRoot string, sideList []string) map[string][]byte {
	snap := map[string][]byte{}
	for _, side := range sideList {
		dir := filepath.Join(codeRoot, side)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_gen.go") {
				continue
			}
			if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				snap[side+"/"+e.Name()] = data
			}
		}
	}
	return snap
}

// ChangedCodeFiles 对比快照，返回内容变化的生成代码文件（含新增）
func ChangedCodeFiles(before map[string][]byte, codeRoot string, sideList []string) []string {
	var changed []string
	for name, oldData := range before {
		newData, err := os.ReadFile(filepath.Join(codeRoot, filepath.FromSlash(name)))
		if err != nil || string(newData) != string(oldData) {
			changed = append(changed, name)
		}
	}
	after := SnapshotCode(codeRoot, sideList)
	for name := range after {
		if _, ok := before[name]; !ok {
			changed = append(changed, name+"(新增)")
		}
	}
	sort.Strings(changed)
	return changed
}

// BaselineDir 返回某端 diff 基线目录（优先外部数据目录，否则用包内内嵌数据目录）
func BaselineDir(dataRoot, codeRoot, side string) string {
	if dataRoot != "" {
		return filepath.Join(dataRoot, side)
	}
	return filepath.Join(codeRoot, side, "data")
}

// BuildDiffReport 生成完整 diff 摘要文本（含各端数据差异与定义文件变更）。
// sideDiffs 必须在写入新产物之前用 DiffSide 采集。
func BuildDiffReport(sideDiffs map[string][]TableDiff, changedCode []string, firstRun bool, sideList []string) string {
	var b strings.Builder
	b.WriteString("── 导表 diff 摘要（对比上次导出）─────────────────\n")
	if firstRun {
		b.WriteString("  首次导出，无基线可对比。\n")
	}
	totalChanges := 0
	for _, side := range sideList {
		diffs := sideDiffs[side]
		if len(diffs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "[%s]\n", side)
		for _, d := range diffs {
			totalChanges++
			writeTableDiff(&b, d)
		}
	}
	if totalChanges == 0 && !firstRun {
		b.WriteString("  数据无变化。\n")
	}
	if len(changedCode) > 0 {
		defs := []string{}
		for _, name := range changedCode {
			// 只挑定义类文件——tables/config 变化通常由数据或 schema 变化引起，已在上面体现
			base := filepath.Base(name)
			if base == "enum_gen.go" || base == "struct_gen.go" {
				defs = append(defs, name)
			}
		}
		if len(defs) > 0 {
			b.WriteString("  定义变更: " + strings.Join(defs, ", ") + "（枚举值/结构体字段有改动，注意行数据 diff 可能看不出来）\n")
		}
	}
	b.WriteString("─────────────────────────────────────────────")
	return b.String()
}

func writeTableDiff(b *strings.Builder, d TableDiff) {
	header := fmt.Sprintf("  %s(%s): ", d.Sheet, d.File)
	if d.IsNewTable {
		fmt.Fprintf(b, "%s新表，%d 行\n", header, len(d.AddedPKs))
		return
	}
	parts := []string{}
	if len(d.SchemaAdded) > 0 {
		parts = append(parts, "新增列 "+strings.Join(d.SchemaAdded, ","))
	}
	if len(d.SchemaGone) > 0 {
		parts = append(parts, "删除列 "+strings.Join(d.SchemaGone, ","))
	}
	fmt.Fprintf(b, "%s+%d行 -%d行 ~%d行", header, len(d.AddedPKs), len(d.RemovedPKs), len(d.Modified))
	if len(parts) > 0 {
		fmt.Fprintf(b, "  [%s]", strings.Join(parts, "; "))
	}
	b.WriteString("\n")

	limit := func(n int) string {
		if n > maxLinesPerSection {
			return fmt.Sprintf("    … 等共 %d 处（-diff-out 可输出完整明细到文件）\n", n)
		}
		return ""
	}
	for i, pk := range d.AddedPKs {
		if i == maxLinesPerSection {
			b.WriteString(limit(len(d.AddedPKs)))
			break
		}
		fmt.Fprintf(b, "    + 主键 %s\n", pk)
	}
	for i, pk := range d.RemovedPKs {
		if i == maxLinesPerSection {
			b.WriteString(limit(len(d.RemovedPKs)))
			break
		}
		fmt.Fprintf(b, "    - 主键 %s\n", pk)
	}
	for i, m := range d.Modified {
		if i == maxLinesPerSection {
			b.WriteString(limit(len(d.Modified)))
			break
		}
		chgs := make([]string, 0, len(m.Changes))
		for _, c := range m.Changes {
			chgs = append(chgs, fmt.Sprintf("%s: %s → %s", c.Field, c.OldStr, c.NewStr))
		}
		fmt.Fprintf(b, "    ~ 主键 %s  %s\n", m.PK, strings.Join(chgs, "; "))
	}
}

// ------------------------------------------------------------ 值格式化

func pkString(v any) string {
	switch x := v.(type) {
	case nil:
		return "<空>"
	case string:
		return x
	case float64:
		if x == math.Trunc(x) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprint(v)
	}
}

// canonJSON 规范化为可比较的 JSON 文本（map 键自动排序；int64 与 float64 的整数值等价）
func canonJSON(v any) string {
	if v == nil {
		return "null"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// prettyVal 面向策划的可读值
func prettyVal(v any) string {
	switch x := v.(type) {
	case nil:
		return "<空>"
	case string:
		return x
	case bool:
		return fmt.Sprint(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case int64:
		return fmt.Sprintf("%d", x)
	default:
		return canonJSON(v) // list/map/struct 用紧凑 JSON 展示
	}
}
