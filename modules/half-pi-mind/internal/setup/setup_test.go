package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInit(t *testing.T) {
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("HOME", tmp)
	}

	env, err := Init()
	if err != nil {
		t.Fatalf("Init() 失败: %v", err)
	}

	// 检查目录是否创建
	for _, d := range []string{env.HomeDir, env.DataDir, env.LogDir, env.SkillsDir} {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			t.Errorf("目录 %s 未创建", d)
		}
	}

	// 检查默认配置文件
	if _, err := os.Stat(env.Config); os.IsNotExist(err) {
		t.Errorf("配置文件 %s 未创建", env.Config)
	}

	// 验证路径正确
	var wantHome string
	if runtime.GOOS == "windows" {
		wantHome = filepath.Join(tmp, "half-pi")
	} else {
		wantHome = filepath.Join(tmp, ".half-pi")
	}
	if env.HomeDir != wantHome {
		t.Errorf("HomeDir = %q, want %q", env.HomeDir, wantHome)
	}
	if env.DBPath != filepath.Join(wantHome, "data", "half-pi.db") {
		t.Errorf("DBPath = %q", env.DBPath)
	}
	if env.EventLog != filepath.Join(wantHome, "logs", "events.jsonl") {
		t.Errorf("EventLog = %q", env.EventLog)
	}
}

func TestInitIdempotent(t *testing.T) {
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("HOME", tmp)
	}

	// 第一次——创建
	_, err := Init()
	if err != nil {
		t.Fatalf("第一次 Init 失败: %v", err)
	}

	// 第二次——不覆盖，不应报错
	_, err = Init()
	if err != nil {
		t.Fatalf("第二次 Init 失败: %v", err)
	}
}

func TestInitConfigContent(t *testing.T) {
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("HOME", tmp)
	}

	env, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if content[:1] != "#" {
		t.Errorf("配置文件应以 # 开头，实际: %q", content[:10])
	}
	if len(content) < 50 {
		t.Errorf("配置文件过短: %d 字节", len(content))
	}
}
