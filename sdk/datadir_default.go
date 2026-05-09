//go:build !android

package sdk

import (
	"os"
	"path/filepath"
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
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		add(&out, filepath.Join(base, "proxysystem-sdk"))
	}
	if base, err := os.UserHomeDir(); err == nil && base != "" {
		add(&out, filepath.Join(base, ".proxysystem-sdk"))
	}
	if td := os.TempDir(); td != "" {
		add(&out, filepath.Join(td, "proxysystem-sdk"))
	}
	add(&out, filepath.Join(".", "proxysystem-sdk"))
	return out
}
