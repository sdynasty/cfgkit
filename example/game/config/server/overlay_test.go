package server

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMain(dir string) (*Config, error) {
	set, err := LoadConfigSetAuto(dir)
	if err != nil {
		return nil, err
	}
	return set.Get("main")
}

func TestLoadAutoEmptyDir(t *testing.T) {
	cfg, err := loadMain(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DescribeSources(); got != "item=embed monster=embed" {
		t.Fatalf("空目录应全内嵌, got: %s", got)
	}
	if cfg.Item.Count() == 0 || cfg.Monster.Count() == 0 {
		t.Fatal("内嵌配置不应为空")
	}
}

func TestLoadAutoSingleFileOverlay(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "server", "main", "item.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main", "item.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadMain(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(src)
	want := "item=external(" + hex.EncodeToString(sum[:])[:8] + ") monster=embed"
	if got := cfg.DescribeSources(); got != want {
		t.Fatalf("单文件 overlay 来源不符, want: %s, got: %s", want, got)
	}
	if cfg.Item.Count() == 0 || cfg.Monster.Count() == 0 {
		t.Fatal("overlay 后两表都应有数据")
	}
}

func TestLoadAutoCorruptFileFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main", "item.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadMain(dir)
	if err == nil {
		t.Fatal("损坏的外部文件应报错，不允许静默回落内嵌")
	}
	if !strings.Contains(err.Error(), "item.json") && !strings.Contains(err.Error(), "Item") {
		t.Fatalf("错误应指明是哪张表: %v", err)
	}
}
