package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ------------------------------------------------------------ 数据模型

// Field 一个配置字段（一列）
type Field struct {
	Name    string // Excel 表头第1行的字段名，同时作为 JSON key
	GoName  string // 生成的 Go 字段名
	Comment string // 表头第3行注释
	Flag    string // 表头第4行: cs / c / s
	Type    *TypeExpr
	Col     int // 1-based 列号
}

// EnumEntry 枚举项
type EnumEntry struct {
	Name    string
	Value   int64
	Comment string
}

// EnumDef 枚举定义（来自 Enum 开头的 sheet）
type EnumDef struct {
	Name        string // Go 类型名 = sheet 名去掉 Enum 前缀
	File, Sheet string
	Entries     []EnumEntry
	valueByName map[string]int64
	nameByValue map[int64]string
}

// Names 返回全部枚举名（按定义顺序）
func (e *EnumDef) Names() []string {
	out := make([]string, 0, len(e.Entries))
	for _, it := range e.Entries {
		out = append(out, it.Name)
	}
	return out
}

// StructDef 内联结构体定义（来自 Struct 开头的 sheet）
type StructDef struct {
	Name        string // Go 类型名 = sheet 名去掉 Struct 前缀
	File, Sheet string
	Fields      []*Field // 复用 Field: Name/GoName/Comment/Type（无 Flag/Col）
	byName      map[string]*Field
}

// FieldNames 返回全部字段名（按定义顺序）
func (s *StructDef) FieldNames() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f.Name)
	}
	return out
}

// RawRow 原始数据行
type RawRow struct {
	ExcelRow int // 1-based，用于报错定位
	Cells    []string
}

// Table 一张配置表（一个 sheet）
type Table struct {
	File, Sheet string
	Fields      []*Field
	PK          *Field // 首列即主键
	Raw         []*RawRow
	Values      []map[string]any // 解析后的值，与 Raw 平行
	pkSeen      map[any]int      // 主键值 -> Excel 行号
}

// PKGoType 主键的 Go 类型
func (t *Table) PKGoType() string {
	switch t.PK.Type.Kind {
	case KInt:
		return "int32"
	case KInt64:
		return "int64"
	default:
		return "string"
	}
}

// Loc 生成给策划看的错误定位: item.xlsx[Item] 第7行D列(price)
func (t *Table) Loc(row, col0 int, field string) string {
	col, _ := excelize.ColumnNumberToName(col0 + 1)
	return fmt.Sprintf("%s[%s] 第%d行%s列(%s)", t.File, t.Sheet, row, col, field)
}

// ------------------------------------------------------------ 第一阶段: 读表头 + 主键

// LoadExcels 扫描目录下所有 xlsx（跳过 ~$/.~ 临时文件），解析表头结构、枚举与结构体定义
func LoadExcels(dir string) ([]*Table, []*EnumDef, []*StructDef, []error) {
	var tables []*Table
	var enums []*EnumDef
	var structs []*StructDef
	var errs []error

	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		// 跳过非 xlsx、Excel 临时文件(~$xxx.xlsx)、WPS/LibreOffice 锁文件(.~xxx.xlsx)等隐藏文件
		if d.IsDir() || !strings.HasSuffix(name, ".xlsx") ||
			strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)

	for _, path := range files {
		f, err := excelize.OpenFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("无法打开 %s: %w", path, err))
			continue
		}
		rel, _ := filepath.Rel(dir, path)
		for _, sheet := range f.GetSheetList() {
			rows, err := f.GetRows(sheet)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s[%s]: 读取失败: %w", rel, sheet, err))
				continue
			}
			switch {
			case strings.HasPrefix(sheet, "Enum"):
				e, es := parseEnumSheet(rel, sheet, rows)
				errs = append(errs, es...)
				if e != nil {
					enums = append(enums, e)
				}
			case strings.HasPrefix(sheet, "Struct"):
				s, es := parseStructSheet(rel, sheet, rows)
				errs = append(errs, es...)
				if s != nil {
					structs = append(structs, s)
				}
			default:
				t, es := parseTableSheet(rel, sheet, rows)
				errs = append(errs, es...)
				if t != nil {
					tables = append(tables, t)
				}
			}
		}
		f.Close()
	}
	return tables, enums, structs, errs
}

func parseEnumSheet(file, sheet string, rows [][]string) (*EnumDef, []error) {
	var errs []error
	if len(rows) < 2 {
		return nil, []error{fmt.Errorf("%s[%s]: 枚举表至少需要表头(name/value/comment)和一条数据", file, sheet)}
	}
	for i, want := range []string{"name", "value", "comment"} {
		got := ""
		if i < len(rows[0]) {
			got = strings.TrimSpace(rows[0][i])
		}
		if got != want {
			return nil, []error{fmt.Errorf("%s[%s]: 表头第%d列应为 %q，实际为 %q", file, sheet, i+1, want, got)}
		}
	}
	def := &EnumDef{
		Name:        strings.TrimPrefix(sheet, "Enum"),
		File:        file,
		Sheet:       sheet,
		valueByName: map[string]int64{},
		nameByValue: map[int64]string{},
	}
	get := func(r, c int) string {
		if c < len(rows[r]) {
			return strings.TrimSpace(rows[r][c])
		}
		return ""
	}
	for r := 1; r < len(rows); r++ {
		name, valStr := get(r, 0), get(r, 1)
		if name == "" && valStr == "" {
			continue // 空行
		}
		if strings.HasPrefix(name, "#") {
			continue // 注释行
		}
		if !isIdent(name) {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 非法枚举名 %q", file, sheet, r+1, name))
			continue
		}
		v, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 枚举值 %q 必须是整数", file, sheet, r+1, valStr))
			continue
		}
		if _, dup := def.valueByName[name]; dup {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 枚举名 %q 重复", file, sheet, r+1, name))
			continue
		}
		if old, dup := def.nameByValue[v]; dup {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 枚举值 %d 与 %q 重复", file, sheet, r+1, v, old))
			continue
		}
		def.valueByName[name] = v
		def.nameByValue[v] = name
		def.Entries = append(def.Entries, EnumEntry{Name: name, Value: v, Comment: get(r, 2)})
	}
	if len(def.Entries) == 0 {
		errs = append(errs, fmt.Errorf("%s[%s]: 枚举表没有任何数据", file, sheet))
	}
	return def, errs
}

// parseStructSheet 解析 Struct 开头的 sheet：表头 1 行(name/type/comment)，其后每行一个字段。
// 字段类型可用任意已支持类型（含嵌套 struct<Y>），但不允许 #uniq/#index 后缀。
func parseStructSheet(file, sheet string, rows [][]string) (*StructDef, []error) {
	var errs []error
	if len(rows) < 2 {
		return nil, []error{fmt.Errorf("%s[%s]: 结构体表至少需要表头(name/type/comment)和一个字段", file, sheet)}
	}
	for i, want := range []string{"name", "type", "comment"} {
		got := ""
		if i < len(rows[0]) {
			got = strings.TrimSpace(rows[0][i])
		}
		if got != want {
			return nil, []error{fmt.Errorf("%s[%s]: 表头第%d列应为 %q，实际为 %q", file, sheet, i+1, want, got)}
		}
	}
	def := &StructDef{
		Name:   strings.TrimPrefix(sheet, "Struct"),
		File:   file,
		Sheet:  sheet,
		byName: map[string]*Field{},
	}
	get := func(r, c int) string {
		if c < len(rows[r]) {
			return strings.TrimSpace(rows[r][c])
		}
		return ""
	}
	for r := 1; r < len(rows); r++ {
		name, typ := get(r, 0), get(r, 1)
		if name == "" && typ == "" {
			continue // 空行
		}
		if strings.HasPrefix(name, "#") {
			continue // 注释行
		}
		if !isIdent(name) {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 非法字段名 %q", file, sheet, r+1, name))
			continue
		}
		if _, dup := def.byName[name]; dup {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 字段名 %q 重复", file, sheet, r+1, name))
			continue
		}
		if typ == "" {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): 类型未填写", file, sheet, r+1, name))
			continue
		}
		te, err := ParseType(typ)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): %w", file, sheet, r+1, name, err))
			continue
		}
		if te.Uniq || te.Index {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): 结构体字段不支持 #uniq/#index", file, sheet, r+1, name))
			continue
		}
		f := &Field{Name: name, GoName: upperCamel(name), Comment: get(r, 2), Type: te}
		def.Fields = append(def.Fields, f)
		def.byName[name] = f
	}
	if len(def.Fields) == 0 {
		errs = append(errs, fmt.Errorf("%s[%s]: 结构体没有任何字段", file, sheet))
	}
	return def, errs
}

func parseTableSheet(file, sheet string, rows [][]string) (*Table, []error) {
	var errs []error
	cell := func(r, c int) string {
		if r < len(rows) && c < len(rows[r]) {
			return strings.TrimSpace(rows[r][c])
		}
		return ""
	}
	if len(rows) < 4 {
		return nil, []error{fmt.Errorf("%s[%s]: 表头不足4行（字段名/类型/注释/导出标记）", file, sheet)}
	}
	t := &Table{File: file, Sheet: sheet, pkSeen: map[any]int{}}

	width := 0
	for _, row := range rows[:4] {
		if len(row) > width {
			width = len(row)
		}
	}
	for c := 0; c < width; c++ {
		name := cell(0, c)
		typ, comment := cell(1, c), cell(2, c)
		flag := strings.ToLower(cell(3, c))
		if name == "" {
			if typ != "" || comment != "" || (flag != "" && flag != "-") {
				col, _ := excelize.ColumnNumberToName(c + 1)
				errs = append(errs, fmt.Errorf("%s[%s] 第1行%s列: 字段名为空但存在类型/注释", file, sheet, col))
			}
			continue
		}
		if !isIdent(name) {
			errs = append(errs, fmt.Errorf("%s[%s] 第1行: 非法字段名 %q（仅字母/数字/下划线）", file, sheet, name))
			continue
		}
		if flag == "" {
			flag = "cs"
		}
		if flag == "-" {
			continue // 该列不导出
		}
		if flag != "cs" && flag != "c" && flag != "s" {
			errs = append(errs, fmt.Errorf("%s[%s] 第4行(%s): 非法导出标记 %q（可选 cs/c/s/-）", file, sheet, name, flag))
			continue
		}
		if typ == "" {
			errs = append(errs, fmt.Errorf("%s[%s] 第2行(%s): 类型未填写", file, sheet, name))
			continue
		}
		te, err := ParseType(typ)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s] 第2行(%s): %w", file, sheet, name, err))
			continue
		}
		for _, f0 := range t.Fields {
			if f0.Name == name {
				errs = append(errs, fmt.Errorf("%s[%s] 第1行: 字段名 %q 重复", file, sheet, name))
			}
		}
		t.Fields = append(t.Fields, &Field{
			Name: name, GoName: upperCamel(name), Comment: comment,
			Flag: flag, Type: te, Col: c + 1,
		})
	}
	if len(t.Fields) == 0 {
		errs = append(errs, fmt.Errorf("%s[%s]: 没有有效字段", file, sheet))
		return nil, errs
	}

	t.PK = t.Fields[0]
	switch t.PK.Type.Kind {
	case KInt, KInt64, KString:
	default:
		errs = append(errs, fmt.Errorf("%s[%s] 第2行(%s): 主键(第1列)类型必须是 int/int64/string", file, sheet, t.PK.Name))
		return nil, errs
	}
	if t.PK.Flag != "cs" {
		errs = append(errs, fmt.Errorf("%s[%s] 第4行(%s): 主键列两端都要用，导出标记必须是 cs（当前为 %s）", file, sheet, t.PK.Name, t.PK.Flag))
		return nil, errs
	}

	// 数据行（第5行起）。'#' 开头为注释行，整行跳过。
	for r := 4; r < len(rows); r++ {
		if strings.HasPrefix(cell(r, 0), "#") {
			continue
		}
		empty := true
		for c := 0; c < len(rows[r]); c++ {
			if cell(r, c) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		raw := &RawRow{ExcelRow: r + 1}
		for _, f := range t.Fields {
			raw.Cells = append(raw.Cells, cell(r, f.Col-1))
		}
		pkRaw := cell(r, t.PK.Col-1)
		if pkRaw == "" {
			errs = append(errs, fmt.Errorf("%s: 主键不能为空", t.Loc(r+1, t.PK.Col-1, t.PK.Name)))
			continue
		}
		pk, err := parsePK(pkRaw, t.PK.Type.Kind)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Loc(r+1, t.PK.Col-1, t.PK.Name), err))
			continue
		}
		if old, dup := t.pkSeen[pk]; dup {
			errs = append(errs, fmt.Errorf("%s: 主键 %v 与第%d行重复", t.Loc(r+1, t.PK.Col-1, t.PK.Name), pk, old))
			continue
		}
		t.pkSeen[pk] = r + 1
		t.Raw = append(t.Raw, raw)
	}
	return t, errs
}

// ------------------------------------------------------------ 第二阶段: 解析值 + 交叉校验

// Context 全量上下文（所有表/枚举/结构体都加载完后才能做 ref/enum/struct 校验）
type Context struct {
	enums   map[string]*EnumDef
	structs map[string]*StructDef
	tables  map[string]*Table // by sheet 名
}

// ResolveAll 解析全部数据行的值，并做枚举/引用/结构体/唯一索引校验
func ResolveAll(tables []*Table, enums []*EnumDef, structs []*StructDef) []error {
	ctx := &Context{
		enums:   map[string]*EnumDef{},
		structs: map[string]*StructDef{},
		tables:  map[string]*Table{},
	}
	var errs []error

	for _, e := range enums {
		if old, dup := ctx.enums[e.Name]; dup {
			errs = append(errs, fmt.Errorf("枚举 %s 重复定义: %s[%s] 与 %s[%s]", e.Name, old.File, old.Sheet, e.File, e.Sheet))
			continue
		}
		ctx.enums[e.Name] = e
	}
	for _, s := range structs {
		if old, dup := ctx.structs[s.Name]; dup {
			errs = append(errs, fmt.Errorf("结构体 %s 重复定义: %s[%s] 与 %s[%s]", s.Name, old.File, old.Sheet, s.File, s.Sheet))
			continue
		}
		ctx.structs[s.Name] = s
	}
	for _, t := range tables {
		if old, dup := ctx.tables[t.Sheet]; dup {
			errs = append(errs, fmt.Errorf("表名 %s 重复: %s 与 %s（sheet 名需全局唯一）", t.Sheet, old.File, t.File))
			continue
		}
		ctx.tables[t.Sheet] = t
	}
	if len(errs) > 0 {
		return errs
	}

	// 类型声明中的 enum<X> / ref<X> / struct<X> 目标必须存在
	for _, t := range tables {
		for _, f := range t.Fields {
			checkTypeTargets(t.File, t.Sheet, f.Name, f.Type, ctx, &errs)
		}
	}
	// 结构体自身字段的类型目标也必须存在
	for _, s := range structs {
		for _, f := range s.Fields {
			checkTypeTargets(s.File, s.Sheet, f.Name, f.Type, ctx, &errs)
		}
	}
	if len(errs) > 0 {
		return errs
	}
	// 结构体不允许循环嵌套（Go 无法生成值类型的递归结构）
	checkStructCycles(ctx, &errs)
	if len(errs) > 0 {
		return errs
	}

	// 逐行解析 + 唯一索引 / 引用存在性校验
	for _, t := range tables {
		uniqSeen := map[string]map[any]int{}
		for _, f := range t.Fields {
			if f.Type.Uniq {
				uniqSeen[f.Name] = map[any]int{}
			}
		}
		for _, raw := range t.Raw {
			vals := map[string]any{}
			for i, f := range t.Fields {
				v, err := parseValue(raw.Cells[i], f.Type, ctx)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", t.Loc(raw.ExcelRow, f.Col-1, f.Name), err))
					continue
				}
				// #uniq 与运行时无条件查重保持一致：不允许空值（空格与显式零值在 JSON 中不可区分）
				if f.Type.Uniq {
					if raw.Cells[i] == "" {
						errs = append(errs, fmt.Errorf("%s: 唯一索引(#uniq)字段不能为空", t.Loc(raw.ExcelRow, f.Col-1, f.Name)))
					} else if old, dup := uniqSeen[f.Name][v]; dup {
						errs = append(errs, fmt.Errorf("%s: 唯一索引值 %v 与第%d行重复", t.Loc(raw.ExcelRow, f.Col-1, f.Name), v, old))
					} else {
						uniqSeen[f.Name][v] = raw.ExcelRow
					}
				}
				vals[f.Name] = v
			}
			t.Values = append(t.Values, vals)
		}
	}
	return errs
}

func checkTypeTargets(file, sheet, fieldName string, te *TypeExpr, ctx *Context, errs *[]error) {
	where := fmt.Sprintf("%s[%s] 字段 %s", file, sheet, fieldName)
	switch te.Kind {
	case KEnum:
		if _, ok := ctx.enums[te.Enum]; !ok {
			*errs = append(*errs, fmt.Errorf("%s: 引用了未定义的枚举 %s（需在 Enum%s sheet 中定义）", where, te.Enum, te.Enum))
		}
	case KRef:
		if _, ok := ctx.tables[te.Ref]; !ok {
			*errs = append(*errs, fmt.Errorf("%s: 引用了不存在的表 %s", where, te.Ref))
		}
	case KStruct:
		if _, ok := ctx.structs[te.Struct]; !ok {
			*errs = append(*errs, fmt.Errorf("%s: 引用了未定义的结构体 %s（需在 Struct%s sheet 中定义）", where, te.Struct, te.Struct))
		}
	case KList:
		checkTypeTargets(file, sheet, fieldName, te.Elem, ctx, errs)
	case KMap:
		checkTypeTargets(file, sheet, fieldName, te.Key, ctx, errs)
		checkTypeTargets(file, sheet, fieldName, te.Val, ctx, errs)
	}
}

// checkStructCycles DFS 检测结构体循环嵌套，如 A -> B -> A
func checkStructCycles(ctx *Context, errs *[]error) {
	const (
		white = 0 // 未访问
		gray  = 1 // 访问中（在当前链上）
		black = 2 // 已完成
	)
	state := map[string]int{}
	var path []string

	var visit func(name string)
	visit = func(name string) {
		def := ctx.structs[name]
		if def == nil || state[name] == black {
			return
		}
		if state[name] == gray {
			// 找到环: 从 path 中定位起点
			start := 0
			for i, n := range path {
				if n == name {
					start = i
					break
				}
			}
			loop := append(append([]string{}, path[start:]...), name)
			*errs = append(*errs, fmt.Errorf("结构体循环嵌套: %s", strings.Join(loop, " -> ")))
			return
		}
		state[name] = gray
		path = append(path, name)
		for _, f := range def.Fields {
			for _, sub := range structRefs(f.Type) {
				visit(sub)
			}
		}
		path = path[:len(path)-1]
		state[name] = black
	}
	names := make([]string, 0, len(ctx.structs))
	for name := range ctx.structs {
		names = append(names, name)
	}
	sort.Strings(names) // 保证报错稳定
	for _, name := range names {
		visit(name)
	}
}

// structRefs 返回类型表达式中直接引用的结构体名（含 list/map 内的）
func structRefs(te *TypeExpr) []string {
	switch te.Kind {
	case KStruct:
		return []string{te.Struct}
	case KList:
		return structRefs(te.Elem)
	case KMap:
		return append(structRefs(te.Key), structRefs(te.Val)...)
	}
	return nil
}

// ------------------------------------------------------------ 单元格值解析

func parsePK(raw string, k Kind) (any, error) {
	if k == KString {
		return raw, nil
	}
	v, err := parseInt(raw)
	if err != nil {
		return nil, fmt.Errorf("主键 %q 不是合法整数", raw)
	}
	return v, nil
}

// parseInt 兼容 Excel 把整数显示成 "1001" / "1001.0" / "1.001E+03" 的情况
func parseInt(raw string) (int64, error) {
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return v, nil
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && f == float64(int64(f)) {
		return int64(f), nil
	}
	return 0, fmt.Errorf("无法解析为整数: %q", raw)
}

// parseValue 按类型解析单元格文本。
// 枚举解析为名字字符串（JSON 中写名字，可读性好，Go 侧 UnmarshalJSON 转回数值）；
// ref 解析为目标表主键值并校验存在性。
func parseValue(raw string, te *TypeExpr, ctx *Context) (any, error) {
	switch te.Kind {
	case KInt, KInt64:
		if raw == "" {
			return int64(0), nil
		}
		return parseInt(raw)
	case KFloat:
		if raw == "" {
			return float64(0), nil
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("无法解析为小数: %q", raw)
		}
		return v, nil
	case KBool:
		switch strings.ToLower(raw) {
		case "", "0", "false", "no", "否":
			return false, nil
		case "1", "true", "yes", "是":
			return true, nil
		}
		return nil, fmt.Errorf("bool 仅接受 1/0、true/false、是/否，收到 %q", raw)
	case KString:
		return raw, nil
	case KEnum:
		def := ctx.enums[te.Enum]
		if def == nil {
			return nil, fmt.Errorf("枚举 %s 未定义", te.Enum)
		}
		if raw == "" {
			return nil, fmt.Errorf("枚举 %s 不能为空（可选值: %s）", te.Enum, strings.Join(def.Names(), "/"))
		}
		if _, ok := def.valueByName[raw]; !ok {
			return nil, fmt.Errorf("%q 不是合法的 %s 枚举值（可选: %s）", raw, te.Enum, strings.Join(def.Names(), "/"))
		}
		return raw, nil
	case KRef:
		target := ctx.tables[te.Ref]
		if target == nil {
			return nil, fmt.Errorf("引用表 %s 不存在", te.Ref)
		}
		if raw == "" {
			if target.PK.Type.Kind == KString {
				return "", nil
			}
			return int64(0), nil
		}
		pk, err := parsePK(raw, target.PK.Type.Kind)
		if err != nil {
			return nil, err
		}
		if _, ok := target.pkSeen[pk]; !ok {
			return nil, fmt.Errorf("引用了不存在的 %s 主键: %v", te.Ref, pk)
		}
		return pk, nil
	case KStruct:
		def := ctx.structs[te.Struct]
		if def == nil {
			return nil, fmt.Errorf("结构体 %s 未定义", te.Struct)
		}
		if raw == "" {
			return nil, nil // 整格留空 = JSON null = Go 零值
		}
		out := make(map[string]any, len(def.Fields))
		for _, part := range strings.Split(raw, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, ":", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("struct 项 %q 缺少 ':'（格式 字段:值;字段:值，%s 共%d个字段全部必填）", part, te.Struct, len(def.Fields))
			}
			fname := strings.TrimSpace(kv[0])
			f := def.byName[fname]
			if f == nil {
				return nil, fmt.Errorf("%q 不是 %s 的字段（可用: %s）", fname, te.Struct, strings.Join(def.FieldNames(), "/"))
			}
			if _, dup := out[fname]; dup {
				return nil, fmt.Errorf("struct %s 字段 %q 重复填写", te.Struct, fname)
			}
			v, err := parseValue(strings.TrimSpace(kv[1]), f.Type, ctx)
			if err != nil {
				return nil, fmt.Errorf("字段 %s: %w", fname, err)
			}
			out[fname] = v
		}
		for _, f := range def.Fields {
			if _, ok := out[f.Name]; !ok {
				return nil, fmt.Errorf("struct %s 缺少字段 %q（全部%d个字段必填: %s）", te.Struct, f.Name, len(def.Fields), strings.Join(def.FieldNames(), "/"))
			}
		}
		return out, nil
	case KList:
		out := []any{}
		if raw == "" {
			return out, nil
		}
		for _, part := range strings.Split(raw, "|") {
			v, err := parseValue(strings.TrimSpace(part), te.Elem, ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case KMap:
		out := map[string]any{}
		if raw == "" {
			return out, nil
		}
		for _, part := range strings.Split(raw, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, ":", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("map 项 %q 缺少 ':'（格式 k:v;k:v）", part)
			}
			k, err := parseMapKey(strings.TrimSpace(kv[0]), te.Key)
			if err != nil {
				return nil, err
			}
			v, err := parseValue(strings.TrimSpace(kv[1]), te.Val, ctx)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("未知类型")
}

func parseMapKey(raw string, te *TypeExpr) (string, error) {
	switch te.Kind {
	case KString:
		if raw == "" {
			return "", fmt.Errorf("map 的键不能为空")
		}
		return raw, nil
	case KInt, KInt64:
		if _, err := parseInt(raw); err != nil {
			return "", fmt.Errorf("map 的键 %q 不是整数", raw)
		}
		return raw, nil
	}
	return "", fmt.Errorf("不支持的 map 键类型")
}
