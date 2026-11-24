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
	if err := os.WriteFile(armSrc, []byte("fake-arm64-dylib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(amdSrc, []byte("fake-amd64-dylib"), 0o644); err != nil {
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
		t.Fatalf("run failed: %v", err)
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
	for _, target := range []string{"darwin-arm64", "darwin-amd64"} {
		f, ok := m.Libraries[0].Files[target]
		if !ok {
			t.Fatalf("missing target %s", target)
		}
		if f.Name != "libmpv.dylib" {
			t.Fatalf("%s name = %q, want libmpv.dylib", target, f.Name)
		}
		if len(f.SHA256) != 64 {
			t.Fatalf("%s invalid sha256 %q", target, f.SHA256)
		}
		if !strings.HasPrefix(f.URL, release+"/libmpv-"+target+"-"+f.SHA256[:12]+".dylib") {
			t.Fatalf("%s unexpected url %q", target, f.URL)
		}
		asset := filepath.Join(outDir, filepath.Base(f.URL))
		if _, err := os.Stat(asset); err != nil {
			t.Fatalf("expected asset %s: %v", asset, err)
		}
	}
}
