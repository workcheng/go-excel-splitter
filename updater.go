package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// version 由构建时注入：go build -ldflags "-X main.version=v1.2.0"
var version = "dev"

const (
	repoOwner        = "workcheng"
	repoName         = "go-excel-splitter"
	latestReleaseAPI = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
	releasesPageURL  = "https://github.com/" + repoOwner + "/" + repoName + "/releases"
	checksumsAsset   = "checksums.txt"
	maxDownloadSize  = 200 << 20
	checkInterval    = 24 * time.Hour
	maxBootAttempts  = 2 // 新版本连续这么多次未能正常启动就回滚
)

// platformAsset 描述某个平台的发布包。
// 注意：名称必须与 .github/workflows/release.yml 保持一致，改名会让旧版本找不到更新。
type platformAsset struct {
	archive string
	binary  string
	arch    string
}

var platformAssets = map[string]platformAsset{
	"linux":   {"excel-splitter-linux.tar.gz", "excel-splitter-linux", "amd64"},
	"darwin":  {"excel-splitter-macos.tar.gz", "excel-splitter-macos", "arm64"},
	"windows": {"excel-splitter-windows.zip", "excel-splitter-windows.exe", "amd64"},
}

// releaseInfo 是一个比当前版本新的发布
type releaseInfo struct {
	Version     string
	Notes       string
	PageURL     string
	AssetURL    string
	ChecksumURL string
	Asset       platformAsset
	// Blocker 非空时不能自动更新，只能提示用户手动下载
	Blocker string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var updateHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

func normalizeVersion(v string) string {
	if v != "" && !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// isNewer 判断 latest 是否严格大于 current，任一版本号不合法都返回 false
func isNewer(latest, current string) bool {
	latest, current = normalizeVersion(latest), normalizeVersion(current)
	return semver.IsValid(latest) && semver.IsValid(current) && semver.Compare(latest, current) > 0
}

// autoApplyBlocker 返回不能自动更新的原因，返回空字符串表示可以自动更新
func autoApplyBlocker(rel *releaseInfo, current, goarch string) string {
	switch {
	case rel.AssetURL == "":
		return "该版本没有当前系统的安装包"
	case rel.ChecksumURL == "":
		return "该版本缺少校验文件"
	case goarch != rel.Asset.arch:
		return fmt.Sprintf("该版本没有 %s 架构的安装包", goarch)
	case semver.Major(normalizeVersion(rel.Version)) != semver.Major(normalizeVersion(current)):
		return "大版本升级，请手动下载安装"
	}
	return ""
}

func newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", repoName+"/"+version)
	return req, nil
}

// doWithRetry 发送 GET 请求，连接失败（国内访问 GitHub 常见的 EOF、超时）时最多重试 3 次。
// 只在拿到响应之前重试，已开始读取的下载不会重来。
func doWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		resp, err := updateHTTPClient.Do(req)
		if err == nil {
			return resp, nil
		}
		if req.Context().Err() != nil {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// checkLatest 查询 GitHub 最新正式版本，没有更新时返回 nil, nil
func checkLatest(ctx context.Context, current string) (*releaseInfo, error) {
	req, err := newRequest(ctx, latestReleaseAPI)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := doWithRetry(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("查询最新版本失败: HTTP %d", resp.StatusCode)
	}

	var gr githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&gr); err != nil {
		return nil, fmt.Errorf("解析版本信息失败: %w", err)
	}
	if !isNewer(gr.TagName, current) {
		return nil, nil
	}
	return buildReleaseInfo(gr, current, runtime.GOOS, runtime.GOARCH), nil
}

func buildReleaseInfo(gr githubRelease, current, goos, goarch string) *releaseInfo {
	rel := &releaseInfo{
		Version: gr.TagName,
		Notes:   gr.Body,
		PageURL: gr.HTMLURL,
		Asset:   platformAssets[goos],
	}
	if rel.PageURL == "" {
		rel.PageURL = releasesPageURL
	}
	for _, a := range gr.Assets {
		switch {
		case a.Name == checksumsAsset:
			rel.ChecksumURL = a.URL
		case rel.Asset.archive != "" && a.Name == rel.Asset.archive:
			rel.AssetURL = a.URL
		}
	}
	rel.Blocker = autoApplyBlocker(rel, current, goarch)
	return rel
}

// parseChecksums 解析 sha256sum 输出：每行 "<hash>  <文件名>"
func parseChecksums(text string) map[string]string {
	sums := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return sums
}

func fetchText(ctx context.Context, url string) (string, error) {
	req, err := newRequest(ctx, url)
	if err != nil {
		return "", err
	}
	resp, err := doWithRetry(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(data), err
}

type progressWriter struct {
	done, total int64
	onProgress  func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.onProgress != nil {
		p.onProgress(p.done, p.total)
	}
	return len(b), nil
}

// downloadUpdate 下载并校验新版本，解压出的可执行文件放在 exe 同目录的 exe+".new"，
// 保证后续替换是同一文件系统内的原子 rename。mirror 非空时作为下载地址前缀，校验文件始终从 GitHub 获取。
func downloadUpdate(ctx context.Context, rel *releaseInfo, exe, mirror string, onProgress func(done, total int64)) (string, error) {
	checksums, err := fetchText(ctx, rel.ChecksumURL)
	if err != nil {
		return "", fmt.Errorf("下载校验文件失败: %w", err)
	}
	want := parseChecksums(checksums)[rel.Asset.archive]
	if want == "" {
		return "", fmt.Errorf("校验文件中没有 %s", rel.Asset.archive)
	}

	url := rel.AssetURL
	if mirror != "" {
		url = strings.TrimRight(mirror, "/") + "/" + url
	}
	req, err := newRequest(ctx, url)
	if err != nil {
		return "", err
	}
	resp, err := doWithRetry(req)
	if err != nil {
		return "", fmt.Errorf("下载更新失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载更新失败: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDownloadSize {
		return "", fmt.Errorf("更新包过大: %d 字节", resp.ContentLength)
	}

	tmp, err := os.CreateTemp("", "excel-splitter-update-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	hash := sha256.New()
	progress := &progressWriter{total: resp.ContentLength, onProgress: onProgress}
	n, err := io.Copy(io.MultiWriter(tmp, hash, progress), io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return "", fmt.Errorf("下载更新失败: %w", err)
	}
	if n > maxDownloadSize {
		return "", errors.New("更新包过大")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return "", fmt.Errorf("文件校验失败，下载内容可能已损坏（期望 %s，实际 %s）", want, got)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	newPath := exe + ".new"
	if err := extractBinary(tmp.Name(), rel.Asset.archive, rel.Asset.binary, newPath); err != nil {
		return "", fmt.Errorf("解压更新失败: %w", err)
	}
	return newPath, nil
}

// extractBinary 从压缩包中只取出文件名为 binaryName 的普通文件，写到固定的 dest，
// 忽略压缩包内的目录结构，从而避免路径穿越。
func extractBinary(archivePath, archiveName, binaryName, dest string) error {
	if strings.HasSuffix(archiveName, ".zip") {
		r, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer r.Close()
		for _, f := range r.File {
			if f.FileInfo().Mode().IsRegular() && path.Base(f.Name) == binaryName {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				return writeExecutable(dest, rc)
			}
		}
		return fmt.Errorf("压缩包中没有 %s", binaryName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("压缩包中没有 %s", binaryName)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == binaryName {
			return writeExecutable(dest, tr)
		}
	}
}

func writeExecutable(dest string, r io.Reader) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxDownloadSize+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > maxDownloadSize {
		err = errors.New("可执行文件过大")
	}
	if err != nil {
		os.Remove(dest)
	}
	return err
}

// currentExecutable 返回当前程序的真实路径（解析符号链接）
func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// checkCanReplace 检查当前程序能否被原地替换
func checkCanReplace(exe string) error {
	if runtime.GOOS == "darwin" && strings.Contains(exe, ".app/Contents/MacOS") {
		return errors.New("macOS 应用包暂不支持自动更新")
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".excel-splitter-write-test-*")
	if err != nil {
		return fmt.Errorf("程序所在目录没有写权限: %w", err)
	}
	f.Close()
	os.Remove(f.Name())
	return nil
}

// applyUpdate 用 newPath 替换 exe。Windows 下运行中的 exe 不能覆盖但可以改名，
// 所以先把当前程序改名为 .old，再把新文件移到原路径；任何一步失败都立即恢复。
func applyUpdate(statePath, exe, newPath, from, to string) error {
	oldPath := exe + ".old"
	os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("备份当前版本失败: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(oldPath, exe)
		os.Remove(newPath)
		return fmt.Errorf("替换程序失败: %w", err)
	}
	// 杀毒软件可能会立即删除新文件
	if _, err := os.Stat(exe); err != nil {
		os.Rename(oldPath, exe)
		return fmt.Errorf("新版本文件不可用，可能被安全软件拦截: %w", err)
	}

	err := modifyUpdateState(statePath, func(s *updateState) {
		s.Pending = &pendingUpdate{From: from, To: to}
	})
	if err != nil {
		// 只影响启动失败时的自动回滚，不影响本次更新
		updateLogf("记录待确认更新失败: %v", err)
	}
	updateLogf("已从 %s 更新到 %s", from, to)
	return nil
}

// restartSelf 以相同参数启动 exe，调用方随后应退出当前进程
func restartSelf(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Start()
}

type pendingUpdate struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Attempts int    `json:"attempts"`
}

// updateState 持久化在用户配置目录下的更新状态和配置
type updateState struct {
	DisableAutoCheck bool           `json:"disable_auto_check"`
	LastCheck        time.Time      `json:"last_check"`
	SkippedVersion   string         `json:"skipped_version"`
	Mirror           string         `json:"mirror"`
	Pending          *pendingUpdate `json:"pending,omitempty"`
	RolledBackFrom   string         `json:"rolled_back_from,omitempty"`
}

var updateStateMu sync.Mutex

func updateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "excel-splitter")
	return dir, os.MkdirAll(dir, 0755)
}

func updateStatePath() (string, error) {
	dir, err := updateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "update-state.json"), nil
}

func loadUpdateState(statePath string) (updateState, error) {
	updateStateMu.Lock()
	defer updateStateMu.Unlock()
	return readUpdateState(statePath)
}

func readUpdateState(statePath string) (updateState, error) {
	var s updateState
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// modifyUpdateState 读取、修改并保存更新状态
func modifyUpdateState(statePath string, fn func(*updateState)) error {
	updateStateMu.Lock()
	defer updateStateMu.Unlock()
	s, err := readUpdateState(statePath)
	if err != nil {
		updateLogf("更新状态文件损坏，已重置: %v", err)
		s = updateState{}
	}
	fn(&s)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0644)
}

// checkPendingUpdate 在 GUI 启动时调用，负责更新后的启动健康检查。
// 新版本连续 maxBootAttempts 次没能调用 markUpdateHealthy 时回滚到 .old，
// 返回 true 表示已回滚，调用方应重启进入旧版本并退出。
func checkPendingUpdate(statePath, exe string) (bool, error) {
	s, err := loadUpdateState(statePath)
	if err != nil {
		return false, err
	}
	oldPath, badPath := exe+".old", exe+".bad"
	if s.Pending == nil {
		os.Remove(oldPath)
		os.Remove(badPath)
		return false, nil
	}

	if s.Pending.Attempts < maxBootAttempts {
		return false, modifyUpdateState(statePath, func(s *updateState) {
			if s.Pending != nil {
				s.Pending.Attempts++
			}
		})
	}

	failed := s.Pending.To
	if _, err := os.Stat(oldPath); err != nil {
		// 没有备份可回滚，放弃
		updateLogf("%s 启动失败，但找不到旧版本备份，无法回滚", failed)
		return false, modifyUpdateState(statePath, func(s *updateState) { s.Pending = nil })
	}
	os.Remove(badPath)
	if err := os.Rename(exe, badPath); err != nil {
		return false, fmt.Errorf("回滚失败: %w", err)
	}
	if err := os.Rename(oldPath, exe); err != nil {
		os.Rename(badPath, exe)
		return false, fmt.Errorf("回滚失败: %w", err)
	}
	updateLogf("%s 连续 %d 次启动失败，已回滚到 %s", failed, maxBootAttempts, s.Pending.From)
	return true, modifyUpdateState(statePath, func(s *updateState) {
		s.Pending = nil
		s.SkippedVersion = failed
		s.RolledBackFrom = failed
	})
}

// markUpdateHealthy 在新版本正常运行一段时间后调用，确认更新成功并清理旧版本
func markUpdateHealthy(statePath, exe string) error {
	s, err := loadUpdateState(statePath)
	if err != nil || s.Pending == nil {
		return err
	}
	if err := modifyUpdateState(statePath, func(s *updateState) { s.Pending = nil }); err != nil {
		return err
	}
	os.Remove(exe + ".old")
	os.Remove(exe + ".bad")
	updateLogf("已确认 %s 运行正常", version)
	return nil
}

// updateLogf 输出到控制台，并追加到用户配置目录下的 update.log
func updateLogf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println("[update]", msg)
	dir, err := updateDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "update.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), msg)
}

// runCLIUpdate 处理命令行 --update，返回进程退出码
func runCLIUpdate() int {
	fmt.Printf("当前版本: %s\n", version)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	rel, err := checkLatest(ctx, version)
	cancel()
	if err != nil {
		fmt.Printf("检查更新失败: %v\n", err)
		return 1
	}
	if rel == nil {
		fmt.Println("已是最新版本")
		return 0
	}
	fmt.Printf("发现新版本: %s\n", rel.Version)
	if rel.Blocker != "" {
		fmt.Printf("无法自动更新（%s），请手动下载: %s\n", rel.Blocker, rel.PageURL)
		return 1
	}

	exe, err := currentExecutable()
	if err == nil {
		err = checkCanReplace(exe)
	}
	if err != nil {
		fmt.Printf("无法自动更新（%v），请手动下载: %s\n", err, rel.PageURL)
		return 1
	}
	statePath, err := updateStatePath()
	if err != nil {
		fmt.Printf("无法访问配置目录: %v\n", err)
		return 1
	}
	state, _ := loadUpdateState(statePath)

	lastPercent := -1
	newPath, err := downloadUpdate(context.Background(), rel, exe, state.Mirror, func(done, total int64) {
		if total <= 0 {
			return
		}
		if p := int(done * 100 / total); p/10 != lastPercent/10 {
			lastPercent = p
			fmt.Printf("下载中... %d%%\n", p)
		}
	})
	if err == nil {
		err = applyUpdate(statePath, exe, newPath, version, rel.Version)
	}
	if err != nil {
		fmt.Printf("更新失败: %v\n", err)
		return 1
	}
	fmt.Printf("已更新到 %s，重新运行程序即可生效\n", rel.Version)
	return 0
}
