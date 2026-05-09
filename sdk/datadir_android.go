//go:build android

package sdk

import (
	"os"
	"path/filepath"
	"strings"
)

func autoSDKDataDirCandidates() []string {
	add := func(out *[]string, p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		for _, x := range *out {
			if x == p {
				return
			}
		}
		*out = append(*out, p)
	}
	var out []string
	if h := strings.TrimSpace(os.Getenv("HOME")); h != "" {
		add(&out, filepath.Join(h, ".proxysystem-sdk"))
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		add(&out, filepath.Join(xdg, "proxysystem-sdk"))
	}
	if t := strings.TrimSpace(os.Getenv("TMPDIR")); t != "" {
		add(&out, filepath.Join(t, "proxysystem-sdk"))
	}
	if d, err := os.UserHomeDir(); err == nil && strings.TrimSpace(d) != "" {
		add(&out, filepath.Join(d, ".proxysystem-sdk"))
	}
	if d, err := os.UserCacheDir(); err == nil && strings.TrimSpace(d) != "" {
		add(&out, filepath.Join(d, "proxysystem-sdk"))
	}
	if d, err := os.UserConfigDir(); err == nil && strings.TrimSpace(d) != "" {
		add(&out, filepath.Join(d, "proxysystem-sdk"))
	}
	if td := strings.TrimSpace(os.TempDir()); td != "" {
		add(&out, filepath.Join(td, "proxysystem-sdk"))
	}
	add(&out, filepath.Join(".", "proxysystem-sdk"))
	return out
}
