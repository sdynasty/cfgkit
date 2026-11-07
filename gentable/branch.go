package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const defaultBranch = "main"

type branchConfig struct {
	Name   string
	Tables []*Table
}

// LoadBranchExcels 按 excel 一级目录加载分支；main 提供完整配置与全部类型定义。
func LoadBranchExcels(dir string) (branches []branchConfig, enums []*EnumDef, structs []*StructDef, warns []string, errs []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil, nil, []error{fmt.Errorf("扫描 Excel 分支目录 %s 失败: %w", dir, err)}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			if !validBranchName(name) {
				errs = append(errs, fmt.Errorf("非法分支目录名 %q：仅允许小写字母、数字、下划线，且首字符为小写字母", name))
				continue
			}
			names = append(names, name)
			continue
		}
		if strings.HasSuffix(name, ".xlsx") {
			errs = append(errs, fmt.Errorf("Excel 根目录不允许直接放置 %s，请移入 main 或具体分支目录", name))
		}
	}
	if len(errs) > 0 {
		return nil, nil, nil, nil, errs
	}

	mainFound := false
	for _, name := range names {
		if name == defaultBranch {
			mainFound = true
			break
		}
	}
	if !mainFound {
		return nil, nil, nil, nil, []error{fmt.Errorf("缺少默认分支目录 %s", filepath.Join(dir, defaultBranch))}
	}

	mainTables, enums, structs, mainWarnings, mainErrs := LoadExcels(filepath.Join(dir, defaultBranch))
	warns = append(warns, mainWarnings...)
	if len(mainErrs) > 0 {
		return nil, nil, nil, warns, mainErrs
	}
	prefixTableFiles(mainTables, defaultBranch)
	prefixEnumFiles(enums, defaultBranch)
	prefixStructFiles(structs, defaultBranch)
	if resolveErrs := ResolveAll(mainTables, enums, structs); len(resolveErrs) > 0 {
		return nil, nil, nil, warns, resolveErrs
	}
	branches = append(branches, branchConfig{Name: defaultBranch, Tables: mainTables})

	for _, name := range names {
		if name == defaultBranch {
			continue
		}
		overlay, branchEnums, branchStructs, branchWarnings, branchErrs := LoadExcels(filepath.Join(dir, name))
		for _, warning := range branchWarnings {
			warns = append(warns, name+"/"+warning)
		}
		if len(branchErrs) > 0 {
			errs = append(errs, branchErrs...)
			continue
		}
		if len(branchEnums) > 0 || len(branchStructs) > 0 {
			errs = append(errs, fmt.Errorf("分支 %s 不允许定义 Enum/Struct，类型定义只能放在 main", name))
			continue
		}
		prefixTableFiles(overlay, name)
		merged, mergeErrs := mergeBranchTables(mainTables, overlay)
		if len(mergeErrs) > 0 {
			errs = append(errs, mergeErrs...)
			continue
		}
		if resolveErrs := ResolveAll(merged, enums, structs); len(resolveErrs) > 0 {
			errs = append(errs, resolveErrs...)
			continue
		}
		branches = append(branches, branchConfig{Name: name, Tables: merged})
	}
	return branches, enums, structs, warns, errs
}

func validBranchName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func mergeBranchTables(mainTables, overlay []*Table) ([]*Table, []error) {
	merged := append([]*Table(nil), mainTables...)
	indexes := make(map[string]int, len(mainTables))
	for i, table := range mainTables {
		indexes[table.Sheet] = i
	}
	seen := map[string]bool{}
	var errs []error
	for _, table := range overlay {
		if seen[table.Sheet] {
			errs = append(errs, fmt.Errorf("分支表 %s 重复定义: %s", table.Sheet, table.File))
			continue
		}
		seen[table.Sheet] = true
		i, ok := indexes[table.Sheet]
		if !ok {
			errs = append(errs, fmt.Errorf("分支表 %s[%s] 在 main 中不存在；新增表必须先加入 main", table.File, table.Sheet))
			continue
		}
		if err := compareTableSchema(mainTables[i], table); err != nil {
			errs = append(errs, err)
			continue
		}
		merged[i] = table
	}
	return merged, errs
}

func compareTableSchema(main, overlay *Table) error {
	if len(main.Fields) != len(overlay.Fields) {
		return fmt.Errorf("分支表 %s[%s] 的字段数与 main 不一致", overlay.File, overlay.Sheet)
	}
	for i, field := range main.Fields {
		other := overlay.Fields[i]
		if field.Name != other.Name || field.Flag != other.Flag || !reflect.DeepEqual(field.Type, other.Type) {
			return fmt.Errorf("分支表 %s[%s] 第%d列与 main 不一致；字段名、类型、顺序和导出标记必须相同",
				overlay.File, overlay.Sheet, i+1)
		}
	}
	return nil
}

func prefixTableFiles(tables []*Table, branch string) {
	for _, table := range tables {
		table.File = branch + "/" + table.File
	}
}

func prefixEnumFiles(enums []*EnumDef, branch string) {
	for _, enum := range enums {
		enum.File = branch + "/" + enum.File
	}
}

func prefixStructFiles(structs []*StructDef, branch string) {
	for _, structDef := range structs {
		structDef.File = branch + "/" + structDef.File
	}
}
