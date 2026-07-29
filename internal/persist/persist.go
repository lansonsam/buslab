// Package persist 负责项目文件与应用设置的读写。
package persist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lansonsam/buslab/internal/model"
)

const (
	FileExtension = ".buslab.json"
	fileVersion   = 1
)

type projectFile struct {
	Version int            `json:"version"`
	Project *model.Project `json:"project"`
}

// Save 以「临时文件 + 重命名」方式写入，避免写坏已有文件。
func Save(path string, p *model.Project) error {
	if p == nil {
		return fmt.Errorf("项目为空")
	}
	if err := model.ValidateProject(p); err != nil {
		return err
	}
	data, err := json.MarshalIndent(projectFile{Version: fileVersion, Project: p}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化项目失败：%w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建目录失败：%w", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存 %s 失败：%w", path, err)
	}
	return nil
}

func Load(path string) (*model.Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%w", path, err)
	}
	var f projectFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	if f.Project == nil {
		return nil, fmt.Errorf("%s 不含项目数据", path)
	}
	if f.Version > fileVersion {
		return nil, fmt.Errorf("项目文件版本 %d 高于当前支持的 %d", f.Version, fileVersion)
	}
	if err := model.ValidateProject(f.Project); err != nil {
		return nil, err
	}
	return f.Project, nil
}

// EnsureExtension 补齐 .buslab.json 后缀。
func EnsureExtension(path string) string {
	if strings.HasSuffix(path, FileExtension) {
		return path
	}
	return strings.TrimSuffix(path, ".json") + FileExtension
}

type Settings struct {
	LastProject string `json:"lastProject"`
	LogLimit    int    `json:"logLimit"`
	DarkTheme   bool   `json:"darkTheme"`
}

func DefaultSettings() Settings {
	return Settings{LogLimit: 5000, DarkTheme: true}
}

// ConfigDir 返回 $XDG_CONFIG_HOME/buslab（或平台等价目录）。
func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "buslab"), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法确定配置目录：%w", err)
	}
	return filepath.Join(base, "buslab"), nil
}

func settingsPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadSettings 读取设置；文件缺失时返回默认值。
func LoadSettings() Settings {
	path, err := settingsPath()
	if err != nil {
		return DefaultSettings()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultSettings()
	}
	s := DefaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings()
	}
	if s.LogLimit <= 0 {
		s.LogLimit = DefaultSettings().LogLimit
	}
	return s
}

func SaveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败：%w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化设置失败：%w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入设置失败：%w", err)
	}
	return nil
}
