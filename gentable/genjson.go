package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// jsonFile 一张表某一端的 JSON 产物记录（写入时顺带登记，供 manifest 汇总）
type jsonFile struct {
	Side string // 端: client/server
	Name string // 表名（sheet 名）
	File string // JSON 文件名
	MD5  string // 内容指纹
	Rows int    // 数据行数
}

// WriteJSONAll 为每个端输出 JSON，写两处:
//
//	<codeRoot>/<side>/data/*.json  内嵌数据（go:embed 编译进二进制，部署无需外部文件）
//	<dataRoot>/<side>/*.json       外部数据（开发期热更调试用；dataRoot 为空则跳过）
//
// 各自只包含该端字段。枚举以名字输出（可读、好 diff），Go 侧由 UnmarshalJSON 转回数值。
// 写入走 write-if-changed（见 writefile.go），返回产物记录供 manifest 汇总。
func WriteJSONAll(tables []*Table, dataRoot, codeRoot string, sideList []string, wc *writeCounter) ([]jsonFile, error) {
	var out []jsonFile
	for _, side := range sideList {
		dirs := []string{filepath.Join(codeRoot, side, "data")}
		if dataRoot != "" {
			dirs = append(dirs, filepath.Join(dataRoot, side))
		}
		for _, dir := range dirs {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
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
				return nil, fmt.Errorf("%s[%s]: JSON 序列化失败: %w", t.File, t.Sheet, err)
			}
			data = append(data, '\n')
			name := toSnake(t.Sheet) + ".json"
			for _, dir := range dirs {
				if err := wc.writeIfChanged(filepath.Join(dir, name), data); err != nil {
					return nil, err
				}
			}
			sum := md5.Sum(data)
			out = append(out, jsonFile{
				Side: side, Name: t.Sheet, File: name,
				MD5: hex.EncodeToString(sum[:]), Rows: len(t.Values),
			})
		}
	}
	return out, nil
}
