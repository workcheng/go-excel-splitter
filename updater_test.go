package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.1.0", "v1.0.0", true},
		{"v1.0.10", "v1.0.9", true},
		{"1.1.0", "v1.0.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.1.0-beta.1", "v1.1.0", false},
		{"v1.1.0", "dev", false},
		{"latest", "v1.0.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestBuildReleaseInfo(t *testing.T) {
	gr := githubRelease{TagName: "v1.2.0", HTMLURL: "https://example/r"}
	gr.Assets = append(gr.Assets,
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{"excel-splitter-windows.zip", "https://example/win.zip"},
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{"checksums.txt", "https://example/sums"},
	)

	rel := buildReleaseInfo(gr, "v1.0.0", "windows", "amd64")
	if rel.Blocker != "" || rel.AssetURL != "https://example/win.zip" || rel.ChecksumURL != "https://example/sums" {
		t.Fatalf("windows/amd64 应可自动更新: %+v", rel)
	}
	if rel := buildReleaseInfo(gr, "v1.0.0", "windows", "arm64"); rel.Blocker == "" {
		t.Error("架构不匹配时不应自动更新")
	}
	if rel := buildReleaseInfo(gr, "v1.0.0", "linux", "amd64"); rel.Blocker == "" {
		t.Error("缺少 linux 安装包时不应自动更新")
	}
	if rel := buildReleaseInfo(gr, "v0.9.0", "windows", "amd64"); rel.Blocker == "" {
		t.Error("大版本升级不应自动更新")
	}
	gr.Assets = gr.Assets[:1]
	if rel := buildReleaseInfo(gr, "v1.0.0", "windows", "amd64"); rel.Blocker == "" {
		t.Error("缺少校验文件时不应自动更新")
	}
}

func TestParseChecksums(t *testing.T) {
	sums := parseChecksums("ABC123  excel-splitter-linux.tar.gz\ndef456 *excel-splitter-windows.zip\n\nbad-line\n")
	if sums["excel-splitter-linux.tar.gz"] != "abc123" || sums["excel-splitter-windows.zip"] != "def456" || len(sums) != 2 {
		t.Fatalf("unexpected: %v", sums)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	f, _ := os.Create(archive)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../evil/excel-splitter-windows.exe")
	w.Write([]byte("new-binary"))
	zw.Close()
	f.Close()

	dest := filepath.Join(dir, "app.exe.new")
	if err := extractBinary(archive, "excel-splitter-windows.zip", "excel-splitter-windows.exe", dest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "new-binary" {
		t.Fatalf("got %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil")); err == nil {
		t.Fatal("不应按压缩包内路径写文件")
	}
	if err := extractBinary(archive, "excel-splitter-windows.zip", "other", dest); err == nil {
		t.Fatal("找不到文件时应报错")
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	f, _ := os.Create(archive)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("linux-binary")
	tw.WriteHeader(&tar.Header{Name: "excel-splitter-linux", Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	f.Close()

	dest := filepath.Join(dir, "app.new")
	if err := extractBinary(archive, "excel-splitter-linux.tar.gz", "excel-splitter-linux", dest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "linux-binary" {
		t.Fatalf("got %q", data)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestApplyUpdateAndHealthy(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	exe := filepath.Join(dir, "app.exe")
	writeFile(t, exe, "v1")
	writeFile(t, exe+".new", "v2")

	if err := applyUpdate(statePath, exe, exe+".new", "v1.0.0", "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, exe) != "v2" || readFile(t, exe+".old") != "v1" || exists(exe+".new") {
		t.Fatal("替换结果不正确")
	}
	if s, _ := loadUpdateState(statePath); s.Pending == nil || s.Pending.To != "v1.1.0" {
		t.Fatalf("应记录待确认更新: %+v", s)
	}

	// 新版本第一次启动并正常运行
	if rolledBack, err := checkPendingUpdate(statePath, exe); rolledBack || err != nil {
		t.Fatalf("第一次启动不应回滚: %v %v", rolledBack, err)
	}
	if err := markUpdateHealthy(statePath, exe); err != nil {
		t.Fatal(err)
	}
	if s, _ := loadUpdateState(statePath); s.Pending != nil {
		t.Fatal("确认后应清除待确认状态")
	}
	if exists(exe + ".old") {
		t.Fatal("确认后应删除旧版本")
	}
}

func TestApplyUpdateRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app.exe")
	writeFile(t, exe, "v1")

	// 新文件不存在，第二步 rename 失败
	if err := applyUpdate(filepath.Join(dir, "state.json"), exe, exe+".missing", "v1.0.0", "v1.1.0"); err == nil {
		t.Fatal("应返回错误")
	}
	if readFile(t, exe) != "v1" || exists(exe+".old") {
		t.Fatal("失败后应恢复原程序")
	}
}

func TestCheckPendingUpdateRollsBack(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	exe := filepath.Join(dir, "app.exe")
	writeFile(t, exe, "v1")
	writeFile(t, exe+".new", "v2")
	if err := applyUpdate(statePath, exe, exe+".new", "v1.0.0", "v1.1.0"); err != nil {
		t.Fatal(err)
	}

	// 连续 maxBootAttempts 次启动都没能调用 markUpdateHealthy
	for i := 0; i < maxBootAttempts; i++ {
		if rolledBack, err := checkPendingUpdate(statePath, exe); rolledBack || err != nil {
			t.Fatalf("第 %d 次启动不应回滚: %v %v", i+1, rolledBack, err)
		}
	}
	rolledBack, err := checkPendingUpdate(statePath, exe)
	if !rolledBack || err != nil {
		t.Fatalf("应回滚: %v %v", rolledBack, err)
	}
	if readFile(t, exe) != "v1" || readFile(t, exe+".bad") != "v2" {
		t.Fatal("回滚结果不正确")
	}
	s, _ := loadUpdateState(statePath)
	if s.Pending != nil || s.SkippedVersion != "v1.1.0" || s.RolledBackFrom != "v1.1.0" {
		t.Fatalf("回滚后状态不正确: %+v", s)
	}

	// 回到旧版本后正常启动，清理失败的版本
	if rolledBack, _ := checkPendingUpdate(statePath, exe); rolledBack || exists(exe+".bad") {
		t.Fatal("回滚后再次启动应清理 .bad")
	}
}

func TestDoWithRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// 模拟连接被断开（客户端收到 EOF）
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := doWithRetry(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() != 2 {
		t.Fatalf("应重试一次，实际请求 %d 次", calls.Load())
	}
}
