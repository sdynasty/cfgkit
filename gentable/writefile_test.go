package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// exportAll 跑一遍完整导出（解析 + JSON + Go + manifest），返回写入统计
func exportAll(t *testing.T, excelDir, codeRoot, dataRoot string) *writeCounter {
	t.Helper()
	tables, enums, structs, _, errs := LoadExcels(excelDir)
	if len(errs) != 0 {
		t.Fatalf("LoadExcels 报错: %v", errs)
	}
	if errs = ResolveAll(tables, enums, structs); len(errs) != 0 {
		t.Fatalf("ResolveAll 报错: %v", errs)
	}
	wc := &writeCounter{}
	jsonFiles, err := WriteJSONAll(tables, dataRoot, codeRoot, allSides, wc)
	if err != nil {
		t.Fatalf("WriteJSONAll: %v", err)
	}
	if err := WriteGoCode(tables, enums, structs, codeRoot, allSides, wc); err != nil {
		t.Fatalf("WriteGoCode: %v", err)
	}
	if err := writeManifest(excelDir, codeRoot, jsonFiles, allSides, wc); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	return wc
}

// listFiles 递归收集目录下全部文件路径
func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// TestWriteIfChangedKeepsMtime 内容相同的产物第二次导出不重写（不刷 mtime），
// 保证下游 hotreload 不会因此做无效热更
func TestWriteIfChangedKeepsMtime(t *testing.T) {
	excelDir := buildDir(t, []wbSpec{
		commonWB(),
		itemWB([]any{1001, "木剑", "White"}),
		monsterWB([]any{2001, "x:1;y:2", "1001"}),
	})
	root := t.TempDir()
	codeRoot, dataRoot := filepath.Join(root, "config"), filepath.Join(root, "data")

	wc1 := exportAll(t, excelDir, codeRoot, dataRoot)
	if wc1.updated == 0 || wc1.unchanged != 0 {
		t.Fatalf("首次导出应全部写入且无跳过，实际 updated=%d unchanged=%d", wc1.updated, wc1.unchanged)
	}

	// 把全部产物的 mtime 统一改到 1 小时前，第二次导出后应保持不变
	//（以 Chtimes 后实际 stat 到的 mtime 为准，兼容不同文件系统的时间精度）
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	var paths []string
	paths = append(paths, listFiles(t, codeRoot)...)
	paths = append(paths, listFiles(t, dataRoot)...)
	mtimes := map[string]time.Time{}
	for _, p := range paths {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		mtimes[p] = fi.ModTime()
	}

	wc2 := exportAll(t, excelDir, codeRoot, dataRoot)
	if wc2.updated != 0 {
		t.Errorf("二次导出不应用任何文件更新，实际 updated=%d", wc2.updated)
	}
	if wc2.unchanged != wc1.updated {
		t.Errorf("二次导出应全部跳过，实际 unchanged=%d（首次写入 %d）", wc2.unchanged, wc1.updated)
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(mtimes[p]) {
			t.Errorf("%s 的 mtime 被刷新了", p)
		}
	}

	// 数据变化后只重写受影响的文件
	dir2 := buildDir(t, []wbSpec{
		commonWB(),
		itemWB([]any{1001, "木剑改", "White"}),
		monsterWB([]any{2001, "x:1;y:2", "1001"}),
	})
	wc3 := exportAll(t, dir2, codeRoot, dataRoot)
	if wc3.updated == 0 || wc3.unchanged == 0 {
		t.Errorf("改动一张表后应只有部分文件更新，实际 updated=%d unchanged=%d", wc3.updated, wc3.unchanged)
	}
}
