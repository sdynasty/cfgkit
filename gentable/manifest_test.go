package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManifestContent manifest.json 的内容与确定性（两次导出字节一致、md5 与磁盘文件吻合）
func TestManifestContent(t *testing.T) {
	excelDir := buildDir(t, []wbSpec{
		commonWB(),
		itemWB([]any{1001, "木剑", "White"}),
	})
	root := t.TempDir()
	codeRoot, dataRoot := filepath.Join(root, "config"), filepath.Join(root, "data")
	exportAll(t, excelDir, codeRoot, dataRoot)

	path := filepath.Join(codeRoot, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != "dev" { // 测试内运行取不到模块版本
		t.Errorf("Version = %q, 期望 dev", m.Version)
	}
	if len(m.Sides) != 2 || m.Sides[0] != "client" || m.Sides[1] != "server" {
		t.Errorf("Sides = %v", m.Sides)
	}
	if len(m.Tables) != 2 { // Item × client/server
		t.Fatalf("Tables = %v", m.Tables)
	}
	itemJSON, err := os.ReadFile(filepath.Join(codeRoot, "server", "data", "item.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(itemJSON)
	found := false
	for _, tb := range m.Tables {
		if tb.Side == "server" && tb.Name == "Item" {
			found = true
			if tb.File != "item.json" || tb.Rows != 1 || tb.MD5 != hex.EncodeToString(sum[:]) {
				t.Errorf("Item/server 记录不符: %+v", tb)
			}
		}
	}
	if !found {
		t.Error("缺少 Item/server 记录")
	}
	if len(m.Sources) != 2 { // common.xlsx / item.xlsx
		t.Fatalf("Sources = %v", m.Sources)
	}
	srcData, _ := os.ReadFile(filepath.Join(excelDir, "common.xlsx"))
	srcSum := md5.Sum(srcData)
	if m.Sources[0].File != "common.xlsx" || m.Sources[0].MD5 != hex.EncodeToString(srcSum[:]) {
		t.Errorf("Sources[0] 不符: %+v", m.Sources[0])
	}
	if len(m.Digest) != 32 {
		t.Errorf("Digest = %q, 期望 32 位 md5", m.Digest)
	}
	if strings.Contains(string(data), "time") || strings.Contains(string(data), "Time") {
		t.Error("manifest 不应包含时间戳字段")
	}

	// 确定性: 二次导出 manifest 字节不变（mtime 保持已由 TestWriteIfChangedKeepsMtime 覆盖）
	exportAll(t, excelDir, codeRoot, dataRoot)
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(data2) {
		t.Error("两次导出的 manifest.json 内容不一致")
	}
}
