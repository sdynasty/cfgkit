package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func placeBranch(t *testing.T, root, name string, wbs []wbSpec) {
	t.Helper()
	dir := buildDir(t, wbs)
	if err := os.Rename(dir, filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadBranchExcels(t *testing.T) {
	root := t.TempDir()
	placeBranch(t, root, "main", []wbSpec{
		commonWB(),
		itemWB([]any{1001, "木剑", "White"}),
		monsterWB([]any{2001, "x:1;y:2", "1001"}),
	})
	placeBranch(t, root, "branch1", []wbSpec{
		itemWB([]any{1001, "分利剑", "Green"}),
	})
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	branches, enums, structs, _, errs := LoadBranchExcels(root)
	if len(errs) != 0 {
		t.Fatalf("LoadBranchExcels 报错: %v", errs)
	}
	if len(branches) != 3 || len(enums) != 1 || len(structs) != 1 {
		t.Fatalf("分支/定义数量不符: branches=%d enums=%d structs=%d", len(branches), len(enums), len(structs))
	}
	branch1 := branches[1]
	if branch1.Name != "branch1" {
		t.Fatalf("branches[1] = %q", branch1.Name)
	}
	if got := findTable(t, branch1.Tables, "Item").Values[0]["name"]; got != "分利剑" {
		t.Fatalf("branch1 Item.name = %v", got)
	}
	if got := len(findTable(t, branch1.Tables, "Monster").Values); got != 1 {
		t.Fatalf("branch1 未继承 main Monster，行数 = %d", got)
	}
	if got := findTable(t, branches[2].Tables, "Item").Values[0]["name"]; got != "木剑" {
		t.Fatalf("空分支应继承 main，Item.name = %v", got)
	}
}

func TestLoadBranchExcelsRejectsInvalidOverride(t *testing.T) {
	t.Run("new table", func(t *testing.T) {
		root := t.TempDir()
		placeBranch(t, root, "main", []wbSpec{commonWB(), itemWB([]any{1001, "木剑", "White"})})
		placeBranch(t, root, "branch1", []wbSpec{monsterWB([]any{2001, "x:1;y:2", "1001"})})
		_, _, _, _, errs := LoadBranchExcels(root)
		if !errorsContain(errs, "在 main 中不存在") {
			t.Fatalf("应拒绝分支新增表: %v", errs)
		}
	})

	t.Run("schema mismatch", func(t *testing.T) {
		root := t.TempDir()
		placeBranch(t, root, "main", []wbSpec{commonWB(), itemWB([]any{1001, "木剑", "White"})})
		placeBranch(t, root, "branch1", []wbSpec{{"item.xlsx", []sheetSpec{{"Item", [][]any{
			{"id", "name", "quality"},
			{"int", "string#uniq", "string"},
			{"编号", "名字", "品质"},
			{"cs", "cs", "cs"},
			{1001, "分利剑", "Green"},
		}}}}})
		_, _, _, _, errs := LoadBranchExcels(root)
		if !errorsContain(errs, "与 main 不一致") {
			t.Fatalf("应拒绝分支修改表结构: %v", errs)
		}
	})
}

func TestLoadBranchExcelsRequiresMain(t *testing.T) {
	root := t.TempDir()
	placeBranch(t, root, "branch1", []wbSpec{itemWB([]any{1001, "木剑", "White"})})
	_, _, _, _, errs := LoadBranchExcels(root)
	if !errorsContain(errs, "缺少默认分支目录") {
		t.Fatalf("缺少 main 时应报错: %v", errs)
	}
}

func TestWriteJSONAllBranches(t *testing.T) {
	root := t.TempDir()
	placeBranch(t, root, "main", []wbSpec{
		commonWB(), itemWB([]any{1001, "木剑", "White"}), monsterWB([]any{2001, "x:1;y:2", "1001"}),
	})
	placeBranch(t, root, "branch1", []wbSpec{itemWB([]any{1001, "分利剑", "Green"})})
	branches, _, _, _, errs := LoadBranchExcels(root)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	outRoot := t.TempDir()
	wc := &writeCounter{}
	if _, err := WriteJSONAll(branches, outRoot, filepath.Join(outRoot, "config"), []string{"server"}, wc); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"main", "branch1"} {
		for _, name := range []string{"item.json", "monster.json"} {
			if _, err := os.Stat(filepath.Join(outRoot, "server", branch, name)); err != nil {
				t.Fatalf("缺少 %s/%s: %v", branch, name, err)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(outRoot, "server", "branch1", "item.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["name"] != "分利剑" {
		t.Fatalf("branch1 产物内容错误: %s", data)
	}
}

func errorsContain(errs []error, text string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), text) {
			return true
		}
	}
	return false
}
