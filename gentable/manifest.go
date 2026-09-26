package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ------------------------------------------------------------ 产物清单 manifest.json
//
// 在 -code 根目录生成 manifest.json（注意不能进 <side>/data/，会干扰内嵌与覆盖目录）。
// 内容确定性强: 不含时间戳，配合 write-if-changed 只在内容真实变化时刷新。
// 运行时加载（Load/LoadEmbedded/LoadAuto）不消费它，纯供版本核对与审计。

// manifestFile 产物清单文件名（固定生成在 -code 根目录）
const manifestFile = "manifest.json"

// manifest 产物清单
type manifest struct {
	Version string           `json:"version"` // cfgkit 模块版本（取不到为 "dev"）
	Sides   []string         `json:"sides"`   // 本次导出的端
	Tables  []manifestTable  `json:"tables"`  // 每张表每端的产物记录
	Sources []manifestSource `json:"sources"` // 每个 excel 源文件
	Digest  string           `json:"digest"`  // 全部表与源文件的整体指纹
}

type manifestTable struct {
	Side string `json:"side"`
	Name string `json:"name"` // 表名（sheet 名）
	File string `json:"file"` // JSON 文件名
	MD5  string `json:"md5"`
	Rows int    `json:"rows"` // 数据行数
}

type manifestSource struct {
	File string `json:"file"` // 相对 -excel 目录的路径（统一为 / 分隔）
	MD5  string `json:"md5"`
}

// manifestVersion 取 cfgkit 模块版本（go run pkg@vX.Y.Z / go install 时可读出），取不到为 "dev"
func manifestVersion() string {
	if v := buildVersion(); v != "(devel)" {
		return v
	}
	return "dev"
}

// writeManifest 生成 manifest.json。源文件清单通过对 excelDir 重新扫描计算（与 LoadExcels 同一套过滤规则）
func writeManifest(excelDir, codeRoot string, files []jsonFile, sideList []string, wc *writeCounter) error {
	m := manifest{
		Version: manifestVersion(),
		Sides:   sideList,
		Tables:  []manifestTable{},
		Sources: []manifestSource{},
	}
	for _, jf := range files {
		m.Tables = append(m.Tables, manifestTable{
			Side: jf.Side, Name: jf.Name, File: jf.File, MD5: jf.MD5, Rows: jf.Rows,
		})
	}

	var sources []string
	if err := filepath.WalkDir(excelDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || !strings.HasSuffix(name, ".xlsx") ||
			strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".") {
			return nil
		}
		sources = append(sources, path)
		return nil
	}); err != nil {
		return fmt.Errorf("扫描 Excel 目录失败: %w", err)
	}
	sort.Strings(sources)
	for _, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", path, err)
		}
		rel, _ := filepath.Rel(excelDir, path)
		sum := md5.Sum(data)
		m.Sources = append(m.Sources, manifestSource{
			File: filepath.ToSlash(rel), MD5: hex.EncodeToString(sum[:]),
		})
	}

	// 整体 digest: 全部表产物与源文件的指纹再取指纹
	h := md5.New()
	for _, t := range m.Tables {
		fmt.Fprintf(h, "%s/%s:%s:%d\n", t.Side, t.Name, t.MD5, t.Rows)
	}
	for _, s := range m.Sources {
		fmt.Fprintf(h, "src:%s:%s\n", s.File, s.MD5)
	}
	m.Digest = hex.EncodeToString(h.Sum(nil))

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest 序列化失败: %w", err)
	}
	data = append(data, '\n')
	return wc.writeIfChanged(filepath.Join(codeRoot, manifestFile), data)
}
