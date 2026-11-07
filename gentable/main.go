// gentable 导表工具：按 excel/main 与 excel/<branch> 读取分支配置，校验后生成完整 JSON 与 Go 代码。
//
// 用法:
//
//	go run github.com/sdynasty/cfgkit/gentable@latest -excel excel -code game/config -data data
//
// 产物（按导出标记 cs/c/s 分端，可用 -sides 只导一端）:
//
//	<code>/<side>/data/<branch>/*.json  内嵌数据（go:embed 编译进二进制）
//	<code>/manifest.json                产物清单（版本/分支/逐表 md5/整体 digest）
//	<data>/<side>/<branch>/*.json       外部数据（-data "" 可关闭）
//
// main 必须包含全部表、Enum 与 Struct；其他一级目录按 sheet 整表覆盖 main，
// 不允许新增表或修改表结构。未覆盖的表从 main 补齐，每个分支都输出完整有效配置。
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
	excelDir := flag.String("excel", "excel", "Excel 分支根目录（必须含 main，其他一级目录为 Branch）")
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

	branches, enums, structs, warns, errs := LoadBranchExcels(*excelDir)
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n✗ 导表失败，共 %d 个错误:\n", len(errs))
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  -", e)
		}
		printWarnings(warns)
		os.Exit(1)
	}

	// 采集 diff 基线必须在写入新产物之前。
	codeSnap := SnapshotCode(*codeRoot, sideList)
	sideDiffs := map[string][]TableDiff{}
	firstRun := true
	for _, side := range sideList {
		for _, branch := range branches {
			baseDir := BaselineDir(*dataRoot, *codeRoot, side, branch.Name)
			if _, err := os.Stat(baseDir); err == nil {
				firstRun = false
			}
			sideDiffs[side] = append(sideDiffs[side], DiffSide(branch.Tables, side, baseDir)...)
		}
	}

	// JSON 同时写入包内 data/ 供 go:embed 内嵌；全部写入走 write-if-changed。
	wc := &writeCounter{}
	jsonFiles, err := WriteJSONAll(branches, *dataRoot, *codeRoot, sideList, wc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ 写 JSON 失败:", err)
		os.Exit(1)
	}
	if err := WriteGoCode(branches, enums, structs, *codeRoot, sideList, wc); err != nil {
		fmt.Fprintln(os.Stderr, "✗ 写 Go 代码失败:", err)
		os.Exit(1)
	}
	if err := writeManifest(*excelDir, *codeRoot, jsonFiles, sideList, wc); err != nil {
		fmt.Fprintln(os.Stderr, "✗ 写 manifest 失败:", err)
		os.Exit(1)
	}

	branchNames := make([]string, 0, len(branches))
	rowCount := 0
	for _, branch := range branches {
		branchNames = append(branchNames, branch.Name)
		for _, t := range branch.Tables {
			rowCount += len(t.Values)
		}
	}
	fmt.Printf("✓ 导表完成: %d 个分支%v, %d 个枚举, %d 个结构体, %d 个端%v, %d 行数据\n",
		len(branches), branchNames, len(enums), len(structs), len(sideList), sideList, rowCount)
	fmt.Printf("  产物写入: %d 个文件更新, %d 个未变化（内容相同，未刷新 mtime）\n", wc.updated, wc.unchanged)
	fmt.Printf("  内嵌 JSON -> %s/{%s}/data/<branch>（go:embed 编译进二进制）\n", *codeRoot, strings.Join(sideList, ","))
	if *dataRoot != "" {
		fmt.Printf("  外部 JSON -> %s/{%s}/<branch>（开发热更/线上覆盖目录）\n", *dataRoot, strings.Join(sideList, ","))
	}
	fmt.Printf("  Go 代码   -> %s/{%s}\n", *codeRoot, strings.Join(sideList, ","))
	fmt.Printf("  产物清单  -> %s/manifest.json（版本 %s）\n", *codeRoot, manifestVersion())
	for _, branch := range branches {
		for _, t := range branch.Tables {
			cn, sn := len(fieldsFor(t, "client")), len(fieldsFor(t, "server"))
			fmt.Printf("  - %s[%s]: %d 行 -> %s/%s.json (client %d 字段 / server %d 字段)\n",
				t.File, t.Sheet, len(t.Values), branch.Name, toSnake(t.Sheet), cn, sn)
		}
	}

	printWarnings(warns)

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
