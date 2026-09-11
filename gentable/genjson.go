package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteJSONAll 为每个端输出 JSON，写两处:
//
//	<codeRoot>/<side>/data/*.json  内嵌数据（go:embed 编译进二进制，部署无需外部文件）
//	<dataRoot>/<side>/*.json       外部数据（开发期热更调试用；dataRoot 为空则跳过）
//
// 各自只包含该端字段。枚举以名字输出（可读、好 diff），Go 侧由 UnmarshalJSON 转回数值。
func WriteJSONAll(tables []*Table, dataRoot, codeRoot string) error {
	for _, side := range sides {
		dirs := []string{filepath.Join(codeRoot, side, "data")}
		if dataRoot != "" {
			dirs = append(dirs, filepath.Join(dataRoot, side))
		}
		for _, dir := range dirs {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		for _, t := range tables {
			fs := fieldsFor(t, side)
			arr := make([]map[string]any, 0, len(t.Values))
			for _, vals := range t.Values {
				m := make(map[string]any, len(fs))
				for _, f := range fs {
					m[f.Name] = vals[f.Name]
				}
				arr = append(arr, m)
			}
			data, err := json.MarshalIndent(arr, "", "  ")
			if err != nil {
				return fmt.Errorf("%s[%s]: JSON 序列化失败: %w", t.File, t.Sheet, err)
			}
			data = append(data, '\n')
			name := toSnake(t.Sheet) + ".json"
			for _, dir := range dirs {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
