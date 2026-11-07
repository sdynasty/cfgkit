package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// jsonFile 一张表某一分支某一端的 JSON 产物记录（写入时顺带登记，供 manifest 汇总）
type jsonFile struct {
	Side   string // 端: client/server
	Branch string // 分支目录名
	Name   string // 表名（sheet 名）
	File   string // 相对数据根目录的 JSON 文件路径
	MD5    string // 内容指纹
	Rows   int    // 数据行数
}

// WriteJSONAll 为每个分支、每个端输出完整有效 JSON：
//
//	<codeRoot>/<side>/data/<branch>/*.json  内嵌数据（go:embed 编译进二进制）
//	<dataRoot>/<side>/<branch>/*.json       外部热更数据（dataRoot 为空则跳过）
//
// 各自只包含该端字段。枚举以名字输出（可读、好 diff），Go 侧由 UnmarshalJSON 转回数值。
// 写入走 write-if-changed（见 writefile.go），并清理生成目录内已删除的 JSON。
func WriteJSONAll(branches []branchConfig, dataRoot, codeRoot string, sideList []string, wc *writeCounter) ([]jsonFile, error) {
	var out []jsonFile
	expected := map[string]bool{}
	for _, side := range sideList {
		for _, branch := range branches {
			dirs := []string{filepath.Join(codeRoot, side, "data", branch.Name)}
			if dataRoot != "" {
				dirs = append(dirs, filepath.Join(dataRoot, side, branch.Name))
			}
			for _, dir := range dirs {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, err
				}
			}
			for _, t := range branch.Tables {
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
					return nil, fmt.Errorf("%s[%s]: JSON 序列化失败: %w", t.File, t.Sheet, err)
				}
				data = append(data, '\n')
				name := toSnake(t.Sheet) + ".json"
				for _, dir := range dirs {
					path := filepath.Join(dir, name)
					expected[path] = true
					if err := wc.writeIfChanged(path, data); err != nil {
						return nil, err
					}
				}
				sum := md5.Sum(data)
				out = append(out, jsonFile{
					Side: side, Branch: branch.Name, Name: t.Sheet,
					File: filepath.ToSlash(filepath.Join(branch.Name, name)),
					MD5:  hex.EncodeToString(sum[:]), Rows: len(t.Values),
				})
			}
		}
	}
	roots := make([]string, 0, len(sideList)*2)
	for _, side := range sideList {
		roots = append(roots, filepath.Join(codeRoot, side, "data"))
		if dataRoot != "" {
			roots = append(roots, filepath.Join(dataRoot, side))
		}
	}
	for _, root := range roots {
		if err := removeStaleJSON(root, expected, wc); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func removeStaleJSON(root string, expected map[string]bool, wc *writeCounter) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || expected[path] {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		wc.updated++
		return nil
	})
}
