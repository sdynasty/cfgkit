package hotreload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecursiveReload(t *testing.T) {
	dir := t.TempDir()
	branchDir := filepath.Join(dir, "branch1")
	if err := os.Mkdir(branchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCfg(t, branchDir, 1)
	load := func(dir string) (*fakeCfg, error) {
		return fakeLoad(filepath.Join(dir, "branch1"))
	}
	m, err := New(dir, load, WithRecursive[fakeCfg]())
	if err != nil {
		t.Fatal(err)
	}
	defer m.StopWatch()

	writeCfg(t, branchDir, 2)
	if !m.changed() {
		t.Fatal("嵌套 JSON 修改未被检测")
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.Get().V; got != 2 {
		t.Fatalf("Reload 后 V = %d, want 2", got)
	}

	if err := os.Remove(filepath.Join(branchDir, "cfg.json")); err != nil {
		t.Fatal(err)
	}
	if !m.changed() {
		t.Fatal("嵌套 JSON 删除未被检测")
	}
	if err := m.Reload(); err == nil {
		t.Fatal("删除配置后 Reload 应失败")
	}
	if got := m.Get().V; got != 2 {
		t.Fatalf("失败后 V = %d, want 保留 2", got)
	}
}
