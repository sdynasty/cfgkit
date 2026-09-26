package main

import (
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ------------------------------------------------------------ 数据模型

// Field 一个配置字段（一列）
type Field struct {
	Name       string // Excel 表头第1行的字段名，同时作为 JSON key
	GoName     string // 生成的 Go 字段名
	Comment    string // 表头第3行注释
	Flag       string // 表头第4行: cs / c / s
	Type       *TypeExpr
	Col        int    // 1-based 列号
	Row        int    // 1-based 定义行号（仅结构体定义表的字段使用，用于报错定位）
	Optional   bool   // 仅结构体字段: 类型标记 type?，单元格可省略该字段
	HasDefault bool   // 仅结构体字段: 类型标记 type?=默认值
	Default    string // 省略时的默认值文本（按字段类型解析）
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

// requiredNames 返回必填字段名（按定义顺序），用于「缺少字段」报错提示
func (s *StructDef) requiredNames() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		if !f.Optional {
			out = append(out, f.Name)
		}
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

// LoadExcels 扫描目录下所有 xlsx（跳过 ~$/.~ 临时文件），解析表头结构、枚举与结构体定义。
// warns 是不阻断导表的告警（如公式无缓存值），由调用方在导表结束时统一打印。
func LoadExcels(dir string) (tables []*Table, enums []*EnumDef, structs []*StructDef, warns []string, errs []error) {
	var files []string
	if werr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
	}); werr != nil {
		errs = append(errs, fmt.Errorf("扫描 Excel 目录 %s 失败: %w", dir, werr))
	}
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
			switch classifySheet(sheet, rows) {
			case kindEnum:
				e, es := parseEnumSheet(rel, sheet, rows)
				errs = append(errs, es...)
				if e != nil {
					enums = append(enums, e)
				}
			case kindStruct:
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
					warns = append(warns, formulaWarnings(f, t)...)
				}
			}
		}
		f.Close()
	}
	return tables, enums, structs, warns, errs
}

// sheetKind sheet 分类
type sheetKind int

const (
	kindTable  sheetKind = iota // 数据表
	kindEnum                    // 枚举定义表
	kindStruct                  // 结构体定义表
)

// classifySheet 判定 sheet 类型：Enum/Struct 前缀 + 首行表头内容双重匹配才算定义表，
// 避免名为 EnumValue/Structure 的数据表被误判。表头只差大小写/空格时仍按定义表处理，
// 交给对应的解析器报出精确的表头错误，而不是退化成莫名其妙的表头不足4行。
func classifySheet(sheet string, rows [][]string) sheetKind {
	header := []string{}
	if len(rows) > 0 {
		for i := 0; i < 3 && i < len(rows[0]); i++ {
			header = append(header, strings.TrimSpace(rows[0][i]))
		}
	}
	// exact 要求三列精确匹配；宽松匹配只要求前两列对得上（第三列错误由解析器报精准错误）
	match := func(col2 string, exact bool) bool {
		if len(header) < 2 {
			return false
		}
		if exact && len(header) < 3 {
			return false
		}
		eq := strings.EqualFold
		if exact {
			eq = func(a, b string) bool { return a == b }
		}
		if !eq(header[0], "name") || !eq(header[1], col2) {
			return false
		}
		return !exact || eq(header[2], "comment")
	}
	switch {
	case strings.HasPrefix(sheet, "Enum") && (match("value", true) || match("value", false)):
		return kindEnum
	case strings.HasPrefix(sheet, "Struct") && (match("type", true) || match("type", false)):
		return kindStruct
	}
	return kindTable
}

// formulaWarnings 检查数据单元格「含公式但没有缓存值」的情况（脚本生成的 xlsx 常见）:
// excelize 只能读到公式的缓存计算值，无缓存时读到空串，会被静默当作零值。
// 只检查值为空且类型为数值/布尔/枚举/必填(#uniq)的单元格，告警不阻断导表。
func formulaWarnings(f *excelize.File, t *Table) []string {
	const maxPerSheet = 10 // 单表最多列出的条数，防止整列公式刷屏
	var warns []string
	total := 0
	for _, raw := range t.Raw {
		for i, fld := range t.Fields {
			if raw.Cells[i] != "" || !formulaSuspect(fld.Type) {
				continue
			}
			axis, err := excelize.CoordinatesToCellName(fld.Col, raw.ExcelRow)
			if err != nil {
				continue
			}
			formula, err := f.GetCellFormula(t.Sheet, axis)
			if err != nil || formula == "" {
				continue
			}
			total++
			if total <= maxPerSheet {
				warns = append(warns, fmt.Sprintf("%s: 单元格含公式 %q 但没有缓存值（文件可能由脚本生成、未经 Excel 计算），已按空/零值处理",
					t.Loc(raw.ExcelRow, fld.Col-1, fld.Name), formula))
			}
		}
	}
	if total > maxPerSheet {
		warns = append(warns, fmt.Sprintf("%s[%s]: … 其余 %d 条公式无缓存值告警略", t.File, t.Sheet, total-maxPerSheet))
	}
	return warns
}

// formulaSuspect 该类型的空单元格值得检查公式缓存（数值/布尔/枚举的空值会被当作零值，#uniq 不能为空）
func formulaSuspect(te *TypeExpr) bool {
	if te.Uniq {
		return true
	}
	switch te.Kind {
	case KInt, KInt64, KFloat, KBool, KEnum:
		return true
	}
	return false
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
	// 枚举名直接用作 Go 类型名，不合法会在编译期才暴露
	if def.Name == "" {
		errs = append(errs, fmt.Errorf("%s[%s]: sheet 名 %q 去掉 Enum 前缀后为空，无法生成类型名", file, sheet, sheet))
	} else if err := validGoTypeName(def.Name); err != nil {
		errs = append(errs, fmt.Errorf("%s[%s]: 枚举名%v（sheet 名去掉 Enum 前缀后用作 Go 类型名）", file, sheet, err))
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
		// 枚举生成 type X int32 常量，越界会在编译/运行期出问题
		if err := checkInt32(v); err != nil {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行: 枚举%v", file, sheet, r+1, err))
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
// 字段类型可带可选标记: type?（单元格可省略该字段，取零值）、type?=默认值（省略时取默认值，
// 默认值按该类型解析，非法则导表报错；JSON 输出时物化默认值）。
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
	// 结构体名直接用作 Go 类型名，不合法会在编译期才暴露
	if def.Name == "" {
		errs = append(errs, fmt.Errorf("%s[%s]: sheet 名 %q 去掉 Struct 前缀后为空，无法生成类型名", file, sheet, sheet))
	} else if err := validGoTypeName(def.Name); err != nil {
		errs = append(errs, fmt.Errorf("%s[%s]: 结构体名%v（sheet 名去掉 Struct 前缀后用作 Go 类型名）", file, sheet, err))
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
		base, optional, defVal, hasDef, err := splitOptional(typ)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): %w", file, sheet, r+1, name, err))
			continue
		}
		te, err := ParseType(base)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): %w", file, sheet, r+1, name, err))
			continue
		}
		if te.Uniq || te.Index {
			errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): 结构体字段不支持 #uniq/#index", file, sheet, r+1, name))
			continue
		}
		f := &Field{
			Name: name, GoName: upperCamel(name), Comment: get(r, 2), Type: te,
			Row: r + 1, Optional: optional, HasDefault: hasDef, Default: defVal,
		}
		def.Fields = append(def.Fields, f)
		def.byName[name] = f
	}
	if len(def.Fields) == 0 {
		errs = append(errs, fmt.Errorf("%s[%s]: 结构体没有任何字段", file, sheet))
	}
	errs = append(errs, checkFieldGoNames(file, sheet, def.Fields)...)
	return def, errs
}

// splitOptional 剥离结构体字段类型的可选标记:
//
//	"int"      -> "int",     必填
//	"int?"     -> "int",     可选（省略取零值）
//	"int?=5"   -> "int",     可选（省略取默认值 5）
func splitOptional(typ string) (base string, optional bool, def string, hasDef bool, err error) {
	if strings.HasSuffix(typ, "?") {
		return strings.TrimSpace(strings.TrimSuffix(typ, "?")), true, "", false, nil
	}
	if i := strings.Index(typ, "?="); i >= 0 {
		base = strings.TrimSpace(typ[:i])
		def = strings.TrimSpace(typ[i+2:])
		if def == "" {
			return "", false, "", false, fmt.Errorf("类型 %q 的默认值不能为空（不要默认值请用 %s?）", typ, base)
		}
		if strings.Contains(base, "?") {
			return "", false, "", false, fmt.Errorf("类型 %q 的可选标记 ? 位置不正确（应为 type? 或 type?=默认值）", typ)
		}
		return base, true, def, true, nil
	}
	if strings.Contains(typ, "?") {
		return "", false, "", false, fmt.Errorf("类型 %q 的可选标记 ? 位置不正确（应为 type? 或 type?=默认值）", typ)
	}
	return typ, false, "", false, nil
}

// checkFieldGoNames 同一表/结构体内字段经 upperCamel 转换后重名要报错
// （如 stack_max 与 stackMax 都得到 StackMax，生成代码无法编译）
func checkFieldGoNames(file, sheet string, fields []*Field) []error {
	var errs []error
	seen := map[string]string{} // GoName -> 原始字段名
	for _, f := range fields {
		if old, dup := seen[f.GoName]; dup {
			errs = append(errs, fmt.Errorf("%s[%s]: 字段 %q 与 %q 转换后的 Go 名相同（都是 %s），请改名字区分",
				file, sheet, old, f.Name, f.GoName))
			continue
		}
		seen[f.GoName] = f.Name
	}
	return errs
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
	// sheet 名直接用作 Go 类型名，不合法会在编译期才暴露
	if err := validGoTypeName(sheet); err != nil {
		errs = append(errs, fmt.Errorf("%s[%s]: sheet 名%v（sheet 名直接用作 Go 类型名）", file, sheet, err))
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
	errs = append(errs, checkFieldGoNames(file, sheet, t.Fields)...)

	t.PK = t.Fields[0]
	// 主键必须是第1列(A列)。首列标记为 - 时 Fields[0] 会落到后面的列，
	// 既与「首列必须为主键」的约定不符，也会让报错文案指错列
	if t.PK.Col != 1 {
		col, _ := excelize.ColumnNumberToName(t.PK.Col)
		errs = append(errs, fmt.Errorf("%s[%s] 第4行A列: 首列被标记为不导出(-)，主键必须放在第1列（当前首个导出字段是 %s 列的 %s）",
			file, sheet, col, t.PK.Name))
		return nil, errs
	}
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
	// 生成代码的包级标识符冲突（跨类别重名、与框架保留名冲突等，否则编译期才炸）
	errs = append(errs, checkGeneratedIdents(tables, enums, structs)...)
	// 不同表 toSnake 后映射到同一 JSON 文件名（如 myTable/MyTable -> my_table.json）
	errs = append(errs, checkJSONFileNames(tables)...)
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
	// 结构体字段的 ?=默认值 合法性（用全量 ctx 解析，支持 enum/ref/list 等类型）
	for _, s := range structs {
		for _, f := range s.Fields {
			if !f.HasDefault {
				continue
			}
			if _, err := parseValue(f.Default, f.Type, ctx); err != nil {
				errs = append(errs, fmt.Errorf("%s[%s] 第%d行(%s): 默认值 %q 非法: %w",
					s.File, s.Sheet, f.Row, f.Name, f.Default, err))
			}
		}
	}
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

// checkGeneratedIdents 检查生成代码的包级标识符冲突:
// 表/枚举/结构体的类型名、表容器与构造函数名、枚举的包级变量名，
// 相互不能重名，也不能与框架保留名（Config/Load 等）冲突——否则编译期才炸且报错不含 sheet 名。
func checkGeneratedIdents(tables []*Table, enums []*EnumDef, structs []*StructDef) []error {
	var errs []error
	idents := map[string]string{} // 标识符 -> 来源描述
	register := func(ident, src string) {
		if old, dup := idents[ident]; dup {
			errs = append(errs, fmt.Errorf("Go 标识符 %q 冲突: %s 与 %s（生成代码无法编译，请重命名其一）", ident, old, src))
			return
		}
		idents[ident] = src
	}
	// 生成代码里的框架级标识符（见 gencode.go 的 config_gen.go/embed_gen.go 模板）
	for _, name := range []string{
		"Config", "TableSource", "Load", "LoadAuto", "LoadEmbedded", "HasExternal",
		"md5hex", "buildConfig", "embeddedJSON",
	} {
		idents[name] = "框架保留标识符（" + name + "）"
	}
	for _, t := range tables {
		src := fmt.Sprintf("表 %s[%s]", t.File, t.Sheet)
		register(t.Sheet, src+" 的行类型")
		register(t.Sheet+"Table", src+" 的容器类型")
		register("new"+t.Sheet+"Table", src+" 的构造函数")
	}
	for _, e := range enums {
		src := fmt.Sprintf("枚举 %s[%s]", e.File, e.Sheet)
		register(e.Name, src+" 的类型")
		// 包级变量 <lower>Names / <lower>Values（lowerFirst 后可能撞车，如枚举 Item 与 item）
		register(lowerFirst(e.Name)+"Names", src+" 的变量")
		register(lowerFirst(e.Name)+"Values", src+" 的变量")
	}
	for _, s := range structs {
		register(s.Name, fmt.Sprintf("结构体 %s[%s] 的类型", s.File, s.Sheet))
	}
	return errs
}

// checkJSONFileNames 不同表 toSnake 后映射到同一 JSON 文件名要报错（列出冲突双方），
// 否则后写覆盖先写，Load 时数据错位
func checkJSONFileNames(tables []*Table) []error {
	var errs []error
	seen := map[string]*Table{} // json 文件名 -> 表
	for _, t := range tables {
		name := toSnake(t.Sheet) + ".json"
		if old, dup := seen[name]; dup {
			errs = append(errs, fmt.Errorf("JSON 文件名 %q 冲突: 表 %s[%s] 与 %s[%s]（sheet 名转 snake_case 后相同，请重命名其一）",
				name, old.File, old.Sheet, t.File, t.Sheet))
			continue
		}
		seen[name] = t
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
	if k == KInt {
		if err := checkInt32(v); err != nil {
			return nil, fmt.Errorf("主键%v", err)
		}
	}
	return v, nil
}

// checkInt32 int 类型生成 Go int32 字段，越界会在运行期静默截断，导表期必须拦住
func checkInt32(v int64) error {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return fmt.Errorf("值 %d 超出 int32 范围(-2147483648~2147483647)", v)
	}
	return nil
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
	case KInt:
		if raw == "" {
			return int64(0), nil
		}
		v, err := parseInt(raw)
		if err != nil {
			return nil, err
		}
		if err := checkInt32(v); err != nil {
			return nil, err
		}
		return v, nil
	case KInt64:
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
		return unescapeCell(raw), nil
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
		for _, part := range splitUnescaped(raw, ';') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kstr, vstr, ok := splitKVUnescaped(part, ':')
			if !ok {
				return nil, fmt.Errorf("struct 项 %q 缺少 ':'（格式 字段:值;字段:值，%s 的必填字段: %s）", part, te.Struct, strings.Join(def.requiredNames(), "/"))
			}
			fname := strings.TrimSpace(kstr)
			f := def.byName[fname]
			if f == nil {
				return nil, fmt.Errorf("%q 不是 %s 的字段（可用: %s）", fname, te.Struct, strings.Join(def.FieldNames(), "/"))
			}
			if _, dup := out[fname]; dup {
				return nil, fmt.Errorf("struct %s 字段 %q 重复填写", te.Struct, fname)
			}
			v, err := parseValue(strings.TrimSpace(vstr), f.Type, ctx)
			if err != nil {
				return nil, fmt.Errorf("字段 %s: %w", fname, err)
			}
			out[fname] = v
		}
		// 必填字段必须全部出现；可选字段省略时物化默认值（type?=默认值）或零值（type?），
		// JSON 始终写出全量字段，Go 侧不需要指针
		var missing []string
		for _, f := range def.Fields {
			if _, ok := out[f.Name]; ok {
				continue
			}
			if f.Optional {
				v, err := structFieldDefault(f, ctx)
				if err != nil {
					return nil, fmt.Errorf("字段 %s: %w", f.Name, err)
				}
				out[f.Name] = v
				continue
			}
			missing = append(missing, f.Name)
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("struct %s 缺少字段 %q（必填字段: %s）", te.Struct, strings.Join(missing, "/"), strings.Join(def.requiredNames(), "/"))
		}
		return out, nil
	case KList:
		out := []any{}
		if raw == "" {
			return out, nil
		}
		for _, part := range splitUnescaped(raw, '|') {
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
		for _, part := range splitUnescaped(raw, ';') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kstr, vstr, ok := splitKVUnescaped(part, ':')
			if !ok {
				return nil, fmt.Errorf("map 项 %q 缺少 ':'（格式 k:v;k:v）", part)
			}
			k, err := parseMapKey(strings.TrimSpace(kstr), te.Key)
			if err != nil {
				return nil, err
			}
			v, err := parseValue(strings.TrimSpace(vstr), te.Val, ctx)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("未知类型")
}

// structFieldDefault 可选字段被省略时的物化值: 有 ?=默认值 按默认值解析，否则取类型零值
func structFieldDefault(f *Field, ctx *Context) (any, error) {
	if f.HasDefault {
		v, err := parseValue(f.Default, f.Type, ctx)
		if err != nil {
			return nil, fmt.Errorf("默认值 %q 非法: %w", f.Default, err)
		}
		return v, nil
	}
	return zeroValue(f.Type, ctx), nil
}

// zeroValue 类型零值（与 Go 生成代码的零值一致；枚举优先用数值 0 对应的名字，保证 JSON 可读）
func zeroValue(te *TypeExpr, ctx *Context) any {
	switch te.Kind {
	case KInt, KInt64:
		return int64(0)
	case KFloat:
		return float64(0)
	case KBool:
		return false
	case KString:
		return ""
	case KEnum:
		if def := ctx.enums[te.Enum]; def != nil {
			if name, ok := def.nameByValue[0]; ok {
				return name
			}
		}
		return int64(0)
	case KRef:
		if t := ctx.tables[te.Ref]; t != nil && t.PK.Type.Kind == KString {
			return ""
		}
		return int64(0)
	case KList:
		return []any{}
	case KMap:
		return map[string]any{}
	case KStruct:
		return nil
	}
	return nil
}

func parseMapKey(raw string, te *TypeExpr) (string, error) {
	switch te.Kind {
	case KString:
		if raw == "" {
			return "", fmt.Errorf("map 的键不能为空")
		}
		return unescapeCell(raw), nil
	case KInt, KInt64:
		v, err := parseInt(raw)
		if err != nil {
			return "", fmt.Errorf("map 的键 %q 不是整数", raw)
		}
		if te.Kind == KInt {
			if err := checkInt32(v); err != nil {
				return "", fmt.Errorf("map 的键%v", err)
			}
		}
		return raw, nil
	}
	return "", fmt.Errorf("不支持的 map 键类型")
}
