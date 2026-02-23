package server

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type githubRelease struct {
	TagName     string               `json:"tag_name"`
	Prerelease  bool                 `json:"prerelease"`
	PublishedAt string               `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
	Draft       bool                 `json:"draft"`
}

type MihomoSystem struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type MihomoInstallInfo struct {
	Repo        string       `json:"repo"`
	Tag         string       `json:"tag"`
	AssetName   string       `json:"assetName"`
	InstalledAt string       `json:"installedAt"`
	BinPath     string       `json:"binPath"`
	System      MihomoSystem `json:"system"`
}

type MihomoStatus struct {
	Repo      string             `json:"repo"`
	System    MihomoSystem       `json:"system"`
	BinPath   string             `json:"binPath"`
	Installed *MihomoInstallInfo `json:"installed"`
}

type MihomoLatest struct {
	Tag         string `json:"tag"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"publishedAt"`
	AssetName   string `json:"assetName"`
	DownloadURL string `json:"downloadUrl"`
}

type MihomoInstaller struct {
	dataDir string
	storage *Storage
	repo    string

	mu          sync.Mutex
	installLock chan struct{}
}

func NewMihomoInstaller(dataDir string, storage *Storage, repo string) *MihomoInstaller {
	if strings.TrimSpace(repo) == "" {
		repo = "MetaCubeX/mihomo"
	}
	return &MihomoInstaller{dataDir: dataDir, storage: storage, repo: repo}
}

func platformToOS(goos string) string {
	if goos == "windows" {
		return "windows"
	}
	return goos
}

func archToArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "386":
		return "386"
	default:
		return goarch
	}
}

func isLikelyGoBuild(name string) bool {
	return strings.Contains(strings.ToLower(name), "-go") && strings.Contains(strings.ToLower(name), "-")
}

func scoreAsset(name, arch string) int {
	score := 0
	lower := strings.ToLower(name)
	if strings.Contains(lower, "compatible") {
		score -= 100
	}
	if isLikelyGoBuild(lower) {
		score -= 5
	}
	if arch == "amd64" {
		switch {
		case strings.Contains(lower, "-amd64-v1-"):
			score += 60
		case strings.Contains(lower, "-amd64-v2-"):
			score += 10
		case strings.Contains(lower, "-amd64-v3-"):
			score += 0
		default:
			score += 20
		}
	}
	if !strings.Contains(lower, "compatible") && !isLikelyGoBuild(lower) {
		score += 2
	}
	return score
}

func pickBestAsset(assets []githubReleaseAsset, osName, arch string) (githubReleaseAsset, error) {
	prefix := fmt.Sprintf("mihomo-%s-%s", platformToOS(osName), arch)
	ext := ".gz"
	if osName == "windows" {
		ext = ".zip"
	}
	candidates := make([]githubReleaseAsset, 0)
	for _, a := range assets {
		if strings.HasPrefix(a.Name, prefix) && strings.HasSuffix(a.Name, ext) {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return githubReleaseAsset{}, fmt.Errorf("未找到适配当前系统的 mihomo 资源：prefix=%s ext=%s", prefix, ext)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return scoreAsset(candidates[i].Name, arch) > scoreAsset(candidates[j].Name, arch)
	})
	return candidates[0], nil
}

func (m *MihomoInstaller) getSystem() MihomoSystem {
	return MihomoSystem{OS: platformToOS(runtime.GOOS), Arch: archToArch(runtime.GOARCH)}
}

func (m *MihomoInstaller) getBinPath() string {
	exe := "mihomo"
	if runtime.GOOS == "windows" {
		exe = "mihomo.exe"
	}
	return filepath.Join(m.dataDir, "bin", exe)
}

func (m *MihomoInstaller) getInstalled() *MihomoInstallInfo {
	var out MihomoInstallInfo
	if err := m.storage.GetJSON("mihomo_install", &out); err != nil {
		return nil
	}
	if out.Tag == "" || out.AssetName == "" || out.BinPath == "" {
		return nil
	}
	return &out
}

func (m *MihomoInstaller) getStatus() MihomoStatus {
	return MihomoStatus{
		Repo:      m.repo,
		System:    m.getSystem(),
		BinPath:   m.getBinPath(),
		Installed: m.getInstalled(),
	}
}

func (m *MihomoInstaller) fetchLatest(includePrerelease bool) (githubRelease, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	headers := map[string]string{
		"user-agent": "proxy-pool",
		"accept":     "application/vnd.github+json",
	}
	if !includePrerelease {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", m.repo)
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return githubRelease{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return githubRelease{}, fmt.Errorf("拉取 release 失败：HTTP %d", resp.StatusCode)
		}
		var out githubRelease
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return githubRelease{}, err
		}
		return out, nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=20", m.repo)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubRelease{}, fmt.Errorf("拉取 releases 失败：HTTP %d", resp.StatusCode)
	}
	var list []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return githubRelease{}, err
	}
	for _, r := range list {
		if !r.Draft {
			return r, nil
		}
	}
	return githubRelease{}, fmt.Errorf("没有找到可用的 release")
}

func (m *MihomoInstaller) getLatestInfo(includePrerelease bool) (MihomoLatest, error) {
	rel, err := m.fetchLatest(includePrerelease)
	if err != nil {
		return MihomoLatest{}, err
	}
	sys := m.getSystem()
	asset, err := pickBestAsset(rel.Assets, sys.OS, sys.Arch)
	if err != nil {
		return MihomoLatest{}, err
	}
	return MihomoLatest{Tag: rel.TagName, Prerelease: rel.Prerelease, PublishedAt: rel.PublishedAt, AssetName: asset.Name, DownloadURL: asset.BrowserDownloadURL}, nil
}

func (m *MihomoInstaller) ensureBinDir() (string, error) {
	dir := filepath.Join(m.dataDir, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (m *MihomoInstaller) downloadToBuffer(url string) ([]byte, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("user-agent", "proxy-pool")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (m *MihomoInstaller) installFromGzip(buf []byte, targetPath string) error {
	gz, err := gzip.NewReader(bytes.NewReader(buf))
	if err != nil {
		return err
	}
	defer gz.Close()
	b, err := io.ReadAll(gz)
	if err != nil {
		return err
	}
	if err := os.WriteFile(targetPath, b, 0o755); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(targetPath, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func findFileInZip(zr *zip.Reader, wanted string) (*zip.File, error) {
	wanted = strings.ToLower(wanted)
	for _, f := range zr.File {
		name := strings.ToLower(filepath.Base(f.Name))
		if name == wanted {
			return f, nil
		}
	}
	return nil, fmt.Errorf("解压后未找到 %s", wanted)
}

func (m *MihomoInstaller) installFromZip(buf []byte, targetPath string) error {
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return err
	}
	wanted := "mihomo"
	if runtime.GOOS == "windows" {
		wanted = "mihomo.exe"
	}
	f, err := findFileInZip(zr, wanted)
	if err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(targetPath, body, 0o755); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(targetPath, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (m *MihomoInstaller) installLatest(includePrerelease bool, force bool) (*MihomoInstallInfo, error) {
	m.mu.Lock()
	if m.installLock != nil {
		ch := m.installLock
		m.mu.Unlock()
		<-ch
		return m.getInstalled(), nil
	}
	m.installLock = make(chan struct{})
	ch := m.installLock
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		close(ch)
		m.installLock = nil
		m.mu.Unlock()
	}()

	if _, err := m.ensureBinDir(); err != nil {
		return nil, err
	}
	latest, err := m.getLatestInfo(includePrerelease)
	if err != nil {
		return nil, err
	}
	installed := m.getInstalled()
	if !force && installed != nil && installed.Tag == latest.Tag {
		if _, err := os.Stat(m.getBinPath()); err == nil {
			return installed, nil
		}
	}
	buf, err := m.downloadToBuffer(latest.DownloadURL)
	if err != nil {
		return nil, err
	}
	binPath := m.getBinPath()
	tmpPath := binPath + ".tmp"
	if strings.HasSuffix(latest.AssetName, ".gz") {
		if err := m.installFromGzip(buf, tmpPath); err != nil {
			return nil, err
		}
	} else if strings.HasSuffix(latest.AssetName, ".zip") {
		if err := m.installFromZip(buf, tmpPath); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("不支持的资源格式：%s", latest.AssetName)
	}

	_ = os.Remove(binPath + ".bak")
	if _, err := os.Stat(binPath); err == nil {
		_ = os.Rename(binPath, binPath+".bak")
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		return nil, err
	}

	info := &MihomoInstallInfo{
		Repo:        m.repo,
		Tag:         latest.Tag,
		AssetName:   latest.AssetName,
		InstalledAt: nowISO(),
		BinPath:     binPath,
		System:      m.getSystem(),
	}
	if err := m.storage.SetJSON("mihomo_install", info); err != nil {
		return nil, err
	}
	return info, nil
}
