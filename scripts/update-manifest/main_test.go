package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunUpdatesManifestAndCopiesAssets(t *testing.T) {
	tmp := t.TempDir()
	armSrc := filepath.Join(tmp, "arm64.dylib")
	amdSrc := filepath.Join(tmp, "amd64.dylib")
	winSrc := filepath.Join(tmp, "mpv-2.dll")
	if err := os.WriteFile(armSrc, []byte("fake-arm64-dylib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(amdSrc, []byte("fake-amd64-dylib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(winSrc, []byte("fake-windows-amd64-dll"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(tmp, "mygo-plugin.json")
	outDir := filepath.Join(tmp, "release")
	source := "https://github.com/mpv-player/mpv/releases/tag/v0.40.0"
	release := "https://github.com/arixbit/mygo-player/releases/download/libmpv-v0.40.0"

	err := run(manifestPath, "mpv", source, release, outDir, map[string]string{
		"darwin-arm64": armSrc,
		"darwin-amd64": amdSrc,
	})
	if err != nil {
		t.Fatalf("run (darwin) failed: %v", err)
	}

	// Second incremental run adds windows-amd64 while preserving darwin entries.
	err = run(manifestPath, "mpv", source, release, outDir, map[string]string{
		"windows-amd64": winSrc,
	})
	if err != nil {
		t.Fatalf("run (windows) failed: %v", err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Libraries) != 1 || m.Libraries[0].Name != "mpv" || m.Libraries[0].Source != source {
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
