// Command update-manifest updates mygo-plugin.json with SHA-256 checksums and
// immutable release asset URLs for built libmpv dynamic libraries, matching
// MyGo's native library publishing convention.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ffi"
)

type manifest struct {
	Libraries []library `json:"libraries"`
}

type library struct {
	Name    string          `json:"name"`
	Source  string          `json:"source,omitempty"`
	Release string          `json:"release,omitempty"`
	Files   map[string]file `json:"files,omitempty"`
}

type file struct {
	Name   string `json:"name"`
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256"`
}

type fileFlag map[string]string

func (f fileFlag) String() string { return fmt.Sprintf("%v", map[string]string(f)) }

func (f fileFlag) Set(v string) error {
	target, path, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(target) == "" || strings.TrimSpace(path) == "" {
		return fmt.Errorf("invalid -file %q: want <goos>-<goarch>=<path>", v)
	}
	f[strings.TrimSpace(target)] = strings.TrimSpace(path)
	return nil
}

func main() {
	manifestPath := flag.String("manifest", "mygo-plugin.json", "path to mygo-plugin.json")
	releaseURL := flag.String("release", "", "base GitHub release download URL (https://github.com/<owner>/<repo>/releases/download/<tag>)")
	sourceURL := flag.String("source", "", "upstream source URL recorded for license provenance")
	outDir := flag.String("out", "dist/release", "output directory for SHA-256 named release assets")
	libName := flag.String("name", "mpv", "library name in mygo-plugin.json")
	inputs := make(fileFlag)
	flag.Var(inputs, "file", "built library mapping <goos>-<goarch>=<path> (repeatable)")
	flag.Parse()

	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "update-manifest: at least one -file <goos>-<goarch>=<path> is required")
		os.Exit(2)
	}
	if err := run(*manifestPath, *libName, *sourceURL, *releaseURL, *outDir, inputs); err != nil {
		fmt.Fprintf(os.Stderr, "update-manifest: %v\n", err)
		os.Exit(1)
	}
}

func run(manifestPath, libName, sourceURL, releaseURL, outDir string, inputs map[string]string) error {
	var m manifest
	if raw, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("parse %s: %w", manifestPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	idx := -1
	for i := range m.Libraries {
		if m.Libraries[i].Name == libName {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.Libraries = append(m.Libraries, library{Name: libName})
		idx = len(m.Libraries) - 1
	}

	lib := &m.Libraries[idx]
	if sourceURL != "" {
		lib.Source = sourceURL
	}
	if releaseURL != "" {
		lib.Release = strings.TrimSuffix(releaseURL, "/")
	}
	if lib.Files == nil {
		lib.Files = map[string]file{}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	for target, src := range inputs {
		goos, goarch, ok := strings.Cut(target, "-")
		if !ok || goos == "" || goarch == "" {
			return fmt.Errorf("invalid target %q: want <goos>-<goarch>", target)
		}
		sum, err := sha256File(src)
		if err != nil {
			return fmt.Errorf("hash %s: %w", src, err)
		}
		inAppName, ext, err := platformLibraryName(libName, goos)
		if err != nil {
			return err
		}
		assetPrefix := strings.TrimSuffix(inAppName, ext)
		assetName := fmt.Sprintf("%s-%s-%s-%s%s", assetPrefix, goos, goarch, sum[:12], ext)
		assetPath := filepath.Join(outDir, assetName)
		if err := copyFile(src, assetPath); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", src, assetPath, err)
		}

		entry := file{
			Name:   inAppName,
			SHA256: sum,
		}
		if lib.Release != "" {
			entry.URL = lib.Release + "/" + assetName
		}
		lib.Files[target] = entry
		fmt.Printf("prepared %s -> %s (sha256=%s)\n", target, assetPath, sum)
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	// Validate generated manifest directly against mygo/ffi parser.
	checkLib := ffi.New(libName, out)
	if checkLib.Name() != libName {
		return fmt.Errorf("ffi manifest check failed for %q", libName)
	}
	if _, err := checkLib.Path(); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid") || strings.Contains(msg, "names no library") {
			return fmt.Errorf("generated manifest rejected by mygo/ffi: %w", err)
		}
	}

	if err := os.WriteFile(manifestPath, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("updated %s\n", manifestPath)
	return nil
}

func platformLibraryName(name, goos string) (fileName, ext string, err error) {
	base := strings.TrimPrefix(name, "lib")
	switch goos {
	case "darwin":
		return "lib" + base + ".dylib", ".dylib", nil
	case "linux":
		return "lib" + base + ".so", ".so", nil
	case "windows":
		if base == "mpv" {
			return "mpv-2.dll", ".dll", nil
		}
		return base + ".dll", ".dll", nil
	default:
		return "", "", fmt.Errorf("unsupported GOOS %q", goos)
	}
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
