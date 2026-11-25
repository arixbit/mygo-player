package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUpdatesManifestAndCopiesAssets(t *testing.T) {
	tmp := t.TempDir()
	armSrc := filepath.Join(tmp, "arm64.dylib")
	amdSrc := filepath.Join(tmp, "amd64.dylib")
	winAmdSrc := filepath.Join(tmp, "mpv-2-amd64.dll")
	winArmSrc := filepath.Join(tmp, "mpv-2-arm64.dll")
	linuxAmdSrc := filepath.Join(tmp, "linux-amd64.so")
	linuxArmSrc := filepath.Join(tmp, "linux-arm64.so")
	for p, data := range map[string]string{
		armSrc:      "fake-arm64-dylib",
		amdSrc:      "fake-amd64-dylib",
		winAmdSrc:   "fake-windows-amd64-dll",
		winArmSrc:   "fake-windows-arm64-dll",
		linuxAmdSrc: "fake-linux-amd64-so",
		linuxArmSrc: "fake-linux-arm64-so",
	} {
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := filepath.Join(tmp, "mygo-plugin.json")
	outDir := filepath.Join(tmp, "release")
	release := "https://github.com/arixbit/mygo-player/releases/download/libmpv-v0.40.0"

	err := run(manifestPath, "mpv", release, outDir, map[string]string{
		"darwin-arm64": armSrc,
		"darwin-amd64": amdSrc,
	})
	if err != nil {
		t.Fatalf("run (darwin) failed: %v", err)
	}

	// Second incremental run adds windows targets while preserving darwin entries.
	err = run(manifestPath, "mpv", release, outDir, map[string]string{
		"windows-amd64": winAmdSrc,
		"windows-arm64": winArmSrc,
	})
	if err != nil {
		t.Fatalf("run (windows) failed: %v", err)
	}

	// Third incremental run adds linux targets while preserving darwin and windows entries.
	err = run(manifestPath, "mpv", release, outDir, map[string]string{
		"linux-amd64": linuxAmdSrc,
		"linux-arm64": linuxArmSrc,
	})
	if err != nil {
		t.Fatalf("run (linux) failed: %v", err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"source"`) {
		t.Fatalf("manifest should not emit unknown \"source\" key:\n%s", raw)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Libraries) != 1 || m.Libraries[0].Name != "mpv" || m.Libraries[0].Release != release {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	for _, tc := range []struct {
		target string
		name   string
		prefix string
		ext    string
	}{
		{"darwin-arm64", "libmpv.dylib", "libmpv", ".dylib"},
		{"darwin-amd64", "libmpv.dylib", "libmpv", ".dylib"},
		{"windows-amd64", "mpv-2.dll", "mpv-2", ".dll"},
		{"windows-arm64", "mpv-2.dll", "mpv-2", ".dll"},
		{"linux-amd64", "libmpv.so", "libmpv", ".so"},
		{"linux-arm64", "libmpv.so", "libmpv", ".so"},
	} {
		f, ok := m.Libraries[0].Files[tc.target]
		if !ok {
			t.Fatalf("missing target %s", tc.target)
		}
		if f.Name != tc.name {
			t.Fatalf("%s name = %q, want %s", tc.target, f.Name, tc.name)
		}
		if len(f.SHA256) != 64 {
			t.Fatalf("%s invalid sha256 %q", tc.target, f.SHA256)
		}
		wantURL := release + "/" + tc.prefix + "-" + tc.target + "-" + f.SHA256[:12] + tc.ext
		if f.URL != wantURL {
			t.Fatalf("%s url = %q, want %q", tc.target, f.URL, wantURL)
		}
		asset := filepath.Join(outDir, filepath.Base(f.URL))
		if _, err := os.Stat(asset); err != nil {
			t.Fatalf("expected asset %s: %v", asset, err)
		}
	}
}
