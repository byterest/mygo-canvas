package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	fetch "github.com/egoist/mygo/internal/download"
)

// pluginFile is what a Go package that is a plugin of MyGo tells the CLI,
// next to its sources: the native libraries it needs, which the apps whose
// packages include it ship among their resources, downloaded once per user. The terminal plugin
// names libghostty-vt so:
//
//	{"libraries": [{"name": "libghostty-vt", "files": {
//		"darwin-arm64": {"name": "libghostty-vt.dylib", "url": "https://…", "sha256": "…"},
//		…
//	}}]}
//
// with a file per platform (GOOS-GOARCH), which the package loads from
// mygo.PathResources at run time. A library that is "optional" is shipped
// to the platforms it has a file for, and the others go without. A file
// may be in an archive: url is then a .tgz, sha256 its sum, and archive
// the path of the library in it, with license the paths of the notices to
// ship next to it, a path ending in / all the files under it, together as
// <name>-LICENSE.txt:
//
//	"windows-amd64": {"name": "pdfium.dll", "url": "https://…/pdfium-win-x64.tgz",
//		"sha256": "…", "archive": "bin/pdfium.dll", "license": ["LICENSE", "licenses/"]}
const pluginFile = "mygo-plugin.json"

type nativeLibraries struct {
	Libraries []struct {
		Name     string                `json:"name"`
		Optional bool                  `json:"optional"`
		Files    map[string]nativeFile `json:"files"`
	} `json:"libraries"`
}

type nativeFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// Archive is the path of the library in the .tgz at URL, if it is in
	// one, and License those of its notices there.
	Archive string   `json:"archive,omitempty"`
	License []string `json:"license,omitempty"`
}

// appResources lists the resources of the app for goos/goarch (see
// resources), with the native libraries its packages need.
func (c *Config) appResources(goos, goarch string) ([]resource, error) {
	natives, err := c.natives(goos, goarch)
	if err != nil {
		return nil, err
	}
	return c.resourcesWith(goos, goarch, natives, reservedNames(c, goos)...)
}

// natives returns the native libraries the packages of the app for
// goos/goarch name in their mygo-plugin.json, downloaded into the user's
// cache, as resources: both architectures of a universal macOS app, which
// combine into one file.
func (c *Config) natives(goos, goarch string) ([]source, error) {
	arch := goarch
	if goarch == "universal" {
		arch = "arm64" // the packages are the same
	}
	var out, stderr bytes.Buffer
	cmd := goCommand(c.root, []string{"GOOS=" + goos, "GOARCH=" + arch}, "list", "-e", "-deps", "-f", "{{.Dir}}", c.Main)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %v\n%s", err, strings.TrimSpace(stderr.String()))
	}
	var list []source
	seen := map[string]bool{}
	for dir := range strings.Lines(out.String()) {
		dir = strings.TrimSpace(dir)
		data, err := os.ReadFile(filepath.Join(dir, pluginFile))
		if errors.Is(err, fs.ErrNotExist) || dir == "" {
			continue
		} else if err != nil {
			return nil, err
		}
		var n nativeLibraries
		if err := json.Unmarshal(data, &n); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, pluginFile), err)
		}
		for _, l := range n.Libraries {
			if seen[l.Name] {
				continue
			}
			seen[l.Name] = true
			pick := func(target string) (string, string, error) {
				f, ok := l.Files[target]
				if !ok {
					return "", "", fmt.Errorf("%s needs %s, which has no build for %s", dir, l.Name, strings.Replace(target, "-", "/", 1))
				}
				path, err := fetchNative(f)
				return f.Name, path, err
			}
			if goarch == "universal" {
				name, arm, err := pick("darwin-arm64")
				if err != nil {
					return nil, err
				}
				amdName, amd, err := pick("darwin-amd64")
				if err != nil {
					return nil, err
				}
				if amdName != name {
					return nil, fmt.Errorf("%s: %s is %s on arm64 but %s on amd64", filepath.Join(dir, pluginFile), l.Name, name, amdName)
				}
				list = append(list, source{name: name, path: arm, amd64: amd, listed: true})
				continue
			}
			if _, ok := l.Files[goos+"-"+goarch]; !ok && l.Optional {
				continue
			}
			name, path, err := pick(goos + "-" + goarch)
			if err != nil {
				return nil, err
			}
			list = append(list, source{name: name, path: path, listed: true})
			if f := l.Files[goos+"-"+goarch]; len(f.License) > 0 {
				list = append(list, source{name: licenseName(f.Name), path: licensePath(path), listed: true})
			}
		}
	}
	return list, nil
}

// fetchNative returns the path of a native library in the user's cache,
// downloading it there first when it is missing:
// <cache>/mygo/natives/<sha256>/<name>, where the packages that load it
// look too when they run outside an app built by the CLI.
func fetchNative(f nativeFile) (string, error) {
	// The SHA-256 and the name are parts of a path: a package may not
	// name another file than the library it downloads.
	sum, err := hex.DecodeString(f.SHA256)
	validName := f.Name != "" && f.Name != "." && f.Name != ".." && filepath.Base(f.Name) == f.Name && !strings.ContainsAny(f.Name, `/\`)
	if f.URL == "" || err != nil || len(sum) != sha256.Size || f.SHA256 != strings.ToLower(f.SHA256) || !validName {
		return "", fmt.Errorf("invalid native library %+v", f)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, "mygo", "natives", f.SHA256, f.Name)
	download := path
	if f.Archive != "" {
		download = filepath.Join(filepath.Dir(path), "archive.tgz")
		if exists(path) && (len(f.License) == 0 || exists(licensePath(path))) {
			return path, nil // taken out of the archive of that sum
		}
	}
	// What the app ships must be what the package names.
	if !fetch.Check(download, f.SHA256) {
		t := con.start("Downloading " + f.Name)
		err = fetch.Cached(context.Background(), downloadFile(t, f.URL, f.SHA256), download)
		if t.end(err, "Downloaded "+f.Name) != nil {
			return "", fmt.Errorf("downloading %s from %s: %w", f.Name, f.URL, err)
		}
	}
	if f.Archive != "" {
		if err := untar(download, f.Archive, path, f.License, licensePath(path)); err != nil {
			return "", fmt.Errorf("%s from %s: %w", f.Name, f.URL, err)
		}
	}
	return path, nil
}

// licenseName is the name a library's notice is shipped by.
func licenseName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + "-LICENSE.txt"
}

// licensePath is where the notice of the library at path is in the cache.
func licensePath(path string) string {
	return filepath.Join(filepath.Dir(path), licenseName(filepath.Base(path)))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// untar takes a library out of a .tgz, to path, and its notices, those
// the patterns name, together to notices. Each is written to a temporary
// file first, so that no other process finds part of one.
func untar(archive, member, path string, patterns []string, notices string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var lib []byte
	found := false
	texts := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data := func() ([]byte, error) { return io.ReadAll(io.LimitReader(tr, 1<<30)) }
		switch {
		case name == member:
			if lib, err = data(); err != nil {
				return err
			}
			found = true
		case noticeOf(name, patterns):
			if texts[name], err = data(); err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("the archive has no %s", member)
	}
	if len(patterns) > 0 {
		if len(texts) == 0 {
			return fmt.Errorf("the archive has none of the notices %v", patterns)
		}
		names := make([]string, 0, len(texts))
		for n := range texts {
			names = append(names, n)
		}
		slices.Sort(names)
		var b bytes.Buffer
		for i, n := range names {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "==== %s ====\n\n%s", n, bytes.TrimSpace(texts[n]))
		}
		b.WriteString("\n")
		if err := writeAtomic(notices, b.Bytes(), 0o644); err != nil {
			return err
		}
	}
	// The library last: when it is there, its notices are.
	return writeAtomic(path, lib, 0o755)
}

// noticeOf reports whether a member of an archive is one of the notices
// the patterns name.
func noticeOf(name string, patterns []string) bool {
	for _, p := range patterns {
		if name == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(name, p)) {
			return true
		}
	}
	return false
}

// writeAtomic writes a file by way of a temporary one renamed onto it.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".untar-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		if exists(path) {
			return nil // another process wrote it meanwhile
		}
	}
	return err
}
