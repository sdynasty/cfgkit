// gentable 导表工具：扫描 Excel 目录，校验后按端生成 JSON 数据与 Go 代码。
//
// 用法:
//
//	go run github.com/sdynasty/cfgkit/gentable@latest -excel excel -code game/config -data data
//
// 产物（按导出标记 cs/c/s 分端，两端完全独立；可用 -sides 只导出指定端）:
//
//	<code>/client/  package client  仅 cs+c 字段
//	<code>/server/  package server  仅 cs+s 字段
//	<code>/<side>/data/*.json       内嵌数据（go:embed 编译进二进制）
//	<code>/manifest.json            产物清单（版本/逐表 md5/整体 digest，运行时不消费）
//	<data>/<side>/*.json            外部数据（开发期热更调试/线上覆盖目录，-data "" 可关闭）
//
// Excel 规范:
//   - 每个 sheet 一张表，sheet 名即表名（全局唯一；Enum 开头且表头为 name/value/comment 的是枚举表，
//     Struct 开头且表头为 name/type/comment 的是结构体表，其余一律按数据表处理）
//   - 数据表固定 4 行表头: 字段名 / 类型 / 中文注释 / 导出标记(cs|c|s|-)
//   - 第 5 行起为数据，首列以 '#' 开头的行是注释行，跳过
//   - 首列必须为主键(int/int64/string)，且导出标记必须为 cs
//   - 类型: int int64 float bool string enum<X> ref<X> struct<X> list<T> map<K,V>
//     可加索引后缀: string#uniq(唯一,不能为空) string#index(普通)
//   - Struct<名字> 开头的 sheet 定义内联结构体（表头 name/type/comment），
//     单元格写法 字段:值;字段:值，整格留空为 Go 零值；
//     字段类型可带可选标记 type?（省略取零值）与 type?=默认值（省略取默认值）
//   - list 用 | 分隔、struct/map 用 ; 和 : 分隔；值里要写分隔符本身时用反斜杠转义:
//     \\ 表示 \，\| 表示 |，\; 表示 ;，\: 表示 :
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// buildVersion 返回模块版本（go run pkg@vX.Y.Z 或 go build 时可读出）
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "(devel)"
}

func main() {
	excelDir := flag.String("excel", "excel", "Excel 目录")
	codeRoot := flag.String("code", "game/config", "Go 代码生成根目录（下分 client/server，另生成 manifest.json）")
	dataRoot := flag.String("data", "data", "外部 JSON 生成根目录（开发调试/覆盖目录用，空字符串则只生成内嵌数据）")
	sidesFlag := flag.String("sides", "client,server", "只生成指定端: client,server 的逗号分隔子集")
	diffOut := flag.String("diff-out", "", "将 diff 摘要另存到该文件（供 CI/提交信息用），空则只打印")
	showVersion := flag.Bool("version", false, "打印版本后退出")
	flag.Parse()

	if *showVersion {
		fmt.Println("gentable", buildVersion())
		return
	}

	sideList, err := parseSides(*sidesFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}

	if _, err := os.Stat(*excelDir); err != nil {
		fmt.Fprintf(os.Stderr, "Excel 目录不可用: %v\n", err)
		os.Exit(1)
	}

	// 第一阶段: 读全部工作簿（表头结构 + 主键集合 + 枚举/结构体定义 + 公式缓存告警）
	tables, enums, structs, warns, errs := LoadExcels(*excelDir)
	// 第二阶段: 解析数据值 + 交叉校验（枚举/引用/结构体/唯一索引/循环嵌套/命名冲突）
	if len(errs) == 0 {
		errs = ResolveAll(tables, enums, structs)
	}
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n✗ 导表失败，共 %d 个错误:\n", len(errs))
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  -", e)
		}
		printWarnings(warns)
		os.Exit(1)
	}

	// 第三阶段: 采集 diff 基线（必须在写入新产物之前！）
	codeSnap := SnapshotCode(*codeRoot, sideList)
	sideDiffs := map[string][]TableDiff{}
	firstRun := true
	for _, side := range sideList {
		baseDir := BaselineDir(*dataRoot, *codeRoot, side)
		if _, err := os.Stat(baseDir); err == nil {
			firstRun = false
		}
		sideDiffs[side] = DiffSide(tables, side, baseDir)
	}

	// 第四阶段: 生成产物（各端各一套；JSON 同时写入包内 data/ 供 go:embed 内嵌；
	// 全部写入走 write-if-changed：内容相同的文件不重写，避免刷 mtime 触发下游无效热更）
	wc := &writeCounter{}
	jsonFiles, err := WriteJSONAll(tables, *dataRoot, *codeRoot, sideList, wc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ 写 JSON 失败:", err)
		os.Exit(1)
	}
	if err := WriteGoCode(tables, enums, structs, *codeRoot, sideList, wc); err != nil {
		fmt.Fprintln(os.Stderr, "✗ 写 Go 代码失败:", err)
		os.Exit(1)
	}
	if err := writeManifest(*excelDir, *codeRoot, jsonFiles, sideList, wc); err != nil {
		fmt.Fprintln(os.Stderr, "✗ 写 manifest 失败:", err)
		os.Exit(1)
	}

	rowCount := 0
	for _, t := range tables {
		rowCount += len(t.Values)
	}
	fmt.Printf("✓ 导表完成: %d 张表(%d 行数据), %d 个枚举, %d 个结构体, %d 个端%v\n",
		len(tables), rowCount, len(enums), len(structs), len(sideList), sideList)
	fmt.Printf("  产物写入: %d 个文件更新, %d 个未变化（内容相同，未刷新 mtime）\n", wc.updated, wc.unchanged)
	fmt.Printf("  内嵌 JSON -> %s/{%s}/data（go:embed 编译进二进制）\n", *codeRoot, strings.Join(sideList, ","))
	if *dataRoot != "" {
		fmt.Printf("  外部 JSON -> %s/{%s}（开发热更/线上覆盖目录）\n", *dataRoot, strings.Join(sideList, ","))
	}
	fmt.Printf("  Go 代码   -> %s/{%s}\n", *codeRoot, strings.Join(sideList, ","))
	fmt.Printf("  产物清单  -> %s/manifest.json（版本 %s）\n", *codeRoot, manifestVersion())
	for _, t := range tables {
		cn, sn := len(fieldsFor(t, "client")), len(fieldsFor(t, "server"))
		fmt.Printf("  - %s[%s]: %d 行 -> %s.json (client %d 字段 / server %d 字段)\n",
			t.File, t.Sheet, len(t.Values), toSnake(t.Sheet), cn, sn)
	}

	// 公式缓存等不阻断告警，导表结束统一打印到 stderr
	printWarnings(warns)

	// diff 摘要（基线是写入前采集的，代码文件对比在写入后）
	changedCode := ChangedCodeFiles(codeSnap, *codeRoot, sideList)
	report := BuildDiffReport(sideDiffs, changedCode, firstRun, sideList)
	fmt.Println(report)
	if *diffOut != "" {
		if err := os.WriteFile(*diffOut, []byte(report+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "✗ 写 diff 文件失败:", err)
			os.Exit(1)
		}
		fmt.Println("diff 摘要已写入:", *diffOut)
	}
}

// printWarnings 不阻断告警统一打印到 stderr（公式无缓存值等）
func printWarnings(warns []string) {
	if len(warns) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n⚠ 导表告警 %d 条（不阻断；建议用 Excel 打开重存，让公式计算出缓存值）:\n", len(warns))
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "  -", w)
	}
}
