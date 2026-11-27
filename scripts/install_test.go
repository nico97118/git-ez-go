// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// Mock only transport/platform discovery. Real shell, tar and checksum utilities
// exercise installation; no test contacts GitHub or invokes a model.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "__installer_helper" {
		switch os.Args[2] {
		case "uname":
			if os.Args[3] == "-s" {
				fmt.Println(os.Getenv("INSTALL_TEST_OS"))
			} else {
				fmt.Println(os.Getenv("INSTALL_TEST_ARCH"))
			}
		case "git":
			if len(os.Args) != 4 || os.Args[3] != "--version" {
				os.Exit(8)
			}
			fmt.Println("git version " + os.Getenv("INSTALL_TEST_GIT"))
		case "curl":
			output, url := "", ""
			for i := 3; i < len(os.Args); i++ {
				if os.Args[i] == "-o" && i+1 < len(os.Args) {
					i++
					output = os.Args[i]
				} else if strings.HasPrefix(os.Args[i], "https://") {
					url = os.Args[i]
				}
			}
			f, err := os.OpenFile(os.Getenv("INSTALL_TEST_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(3)
			}
			fmt.Fprintln(f, url)
			f.Close()
			if os.Getenv("INSTALL_TEST_INTERRUPT") == "1" {
				syscall.Kill(os.Getppid(), syscall.SIGTERM)
				os.Exit(7)
			}
			info, statErr := os.Stat(filepath.Dir(output))
			if statErr != nil || info.Mode().Perm()&0077 != 0 {
				os.Exit(6)
			}
			fixture := os.Getenv("INSTALL_TEST_FIXTURE")
			var source string
			switch {
			case strings.Contains(url, "/releases?per_page=100&page=1"):
				source = filepath.Join(fixture, "releases.json")
			case strings.Contains(url, "/releases?per_page=100&page=2"):
				source = filepath.Join(fixture, "page2.json")
			case strings.Contains(url, "/releases/tags/"):
				source = filepath.Join(fixture, "tag-"+filepath.Base(url)+".json")
			case strings.Contains(url, "/releases/download/"):
				parts := strings.Split(url, "/")
				source = filepath.Join(fixture, parts[len(parts)-2], parts[len(parts)-1])
			default:
				os.Exit(9)
			}
			data, err := os.ReadFile(source)
			if err != nil {
				os.Exit(22)
			}
			if err = os.WriteFile(output, data, 0600); err != nil {
				os.Exit(3)
			}
		default:
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type release struct {
	Tag       string              `json:"tag_name"`
	Draft     bool                `json:"draft"`
	Pre       bool                `json:"prerelease"`
	Published string              `json:"published_at"`
	Assets    []map[string]string `json:"assets"`
	Body      string              `json:"body"`
}
type installerFixture struct {
	t                                       *testing.T
	root, home, bin, temporary, log, script string
	releases                                []release
	system, arch, git                       string
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func put(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
func newInstaller(t *testing.T) *installerFixture {
	t.Helper()
	root := t.TempDir()
	f := &installerFixture{t: t, root: root, home: root + "/home with spaces", bin: root + "/tools", temporary: root + "/temporary", log: root + "/requests", system: "Linux", arch: "x86_64", git: "2.46.0"}
	for _, p := range []string{f.home, f.bin, f.temporary} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	f.script = root + "/install.sh"
	put(t, f.script, script, 0700)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"curl", "uname", "git"} {
		put(t, f.bin+"/"+name, []byte("#!/bin/sh\nexec "+quote(self)+" __installer_helper "+name+" \"$@\"\n"), 0700)
	}
	return f
}
func (f *installerFixture) add(tag string, pre, draft bool, date string) {
	f.t.Helper()
	dir := f.root + "/" + tag
	if err := os.Mkdir(dir, 0700); err != nil {
		f.t.Fatal(err)
	}
	r := release{Tag: tag, Pre: pre, Draft: draft, Published: date, Body: "Escapes: \"draft\":false, slash /, backslash \\, newline\nUnicode: é"}
	var sums strings.Builder
	for _, target := range []string{"linux_amd64", "linux_arm64", "freebsd_amd64", "freebsd_arm64", "darwin_amd64", "darwin_arm64"} {
		name := "git-ez_" + tag + "_" + target
		filename := name + ".tar.gz"
		files := map[string]string{"git-ez": "#!/bin/sh\nprintf '%s\\n' 'git-ez " + tag + "'\n", "docs/man/git-ez.1": ".TH GIT-EZ 1\n", "LICENSE": "GPL-3.0-only\n", "NOTICE": "fixture\n", "SOURCE.txt": "matching source in the same release\n", "BUILD.json": "{}\n", "SBOM.cdx.json": "{}\n", "DEPENDENCIES.json": "[]\n", "docs/installation.md": "fixture\n"}
		data := testArchive(f.t, name, files, nil)
		put(f.t, dir+"/"+filename, data, 0600)
		sum := sha256.Sum256(data)
		fmt.Fprintf(&sums, "%x  %s\n", sum, filename)
		r.Assets = append(r.Assets, map[string]string{"name": filename})
	}
	put(f.t, dir+"/SHA256SUMS", []byte(sums.String()), 0600)
	r.Assets = append(r.Assets, map[string]string{"name": "SHA256SUMS"}, map[string]string{"name": "git-ez_" + tag + "_source.tar.gz"})
	f.releases = append(f.releases, r)
	f.metadata()
}
func (f *installerFixture) metadata() {
	f.t.Helper()
	data, err := json.Marshal(f.releases)
	if err != nil {
		f.t.Fatal(err)
	}
	put(f.t, f.root+"/releases.json", data, 0600)
	for _, r := range f.releases {
		data, _ = json.Marshal(r)
		put(f.t, f.root+"/tag-"+r.Tag+".json", data, 0600)
	}
}
func testArchive(t *testing.T, root string, files map[string]string, extra *tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		mode := int64(0644)
		if name == "git-ez" {
			mode = 0755
		}
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Size: int64(len(content)), Mode: mode, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		if err := tw.WriteHeader(extra); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func (f *installerFixture) env() []string {
	e := []string{}
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if k != "HOME" && k != "TMPDIR" && k != "PATH" && !strings.HasPrefix(k, "INSTALL_TEST_") {
			e = append(e, v)
		}
	}
	return append(e, "HOME="+f.home, "TMPDIR="+f.temporary, "PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "INSTALL_TEST_OS="+f.system, "INSTALL_TEST_ARCH="+f.arch, "INSTALL_TEST_GIT="+f.git, "INSTALL_TEST_FIXTURE="+f.root, "INSTALL_TEST_LOG="+f.log, "GORACE=atexit_sleep_ms=0")
}
func (f *installerFixture) run(piped bool, args ...string) (string, error) {
	f.t.Helper()
	var cmd *exec.Cmd
	if piped {
		cmd = exec.Command("sh", append([]string{"-s", "--"}, args...)...)
		data, err := os.ReadFile(f.script)
		if err != nil {
			f.t.Fatal(err)
		}
		cmd.Stdin = bytes.NewReader(data)
	} else {
		cmd = exec.Command(f.script, args...)
	}
	cmd.Env = f.env()
	cmd.Dir = f.root
	out, err := cmd.CombinedOutput()
	entries, readErr := os.ReadDir(f.temporary)
	if readErr != nil || len(entries) != 0 {
		f.t.Fatalf("temporary files leaked: %v %v", entries, readErr)
	}
	return string(out), err
}
func (f *installerFixture) installed(prefix, tag string) {
	f.t.Helper()
	if prefix == "" {
		prefix = f.home + "/.local"
	}
	out, err := exec.Command(prefix+"/bin/git-ez", "--version").Output()
	if err != nil || string(out) != "git-ez "+tag+"\n" {
		f.t.Fatal(string(out), err)
	}
	info, err := os.Stat(prefix + "/share/man/man1/git-ez.1")
	if err != nil || info.Size() == 0 || info.Mode().Perm() != 0644 {
		f.t.Fatal(info, err)
	}
	files, err := filepath.Glob(prefix + "/bin/.git-ez.*")
	if err != nil || len(files) != 0 {
		f.t.Fatal(files, err)
	}
}
func (f *installerFixture) requests() string {
	f.t.Helper()
	data, _ := os.ReadFile(f.log)
	return string(data)
}
func TestInstallerPlatformMappings(t *testing.T) {
	for _, target := range []struct{ os, arch, name string }{{"Linux", "x86_64", "linux_amd64"}, {"Linux", "aarch64", "linux_arm64"}, {"FreeBSD", "amd64", "freebsd_amd64"}, {"FreeBSD", "arm64", "freebsd_arm64"}, {"Darwin", "x86_64", "darwin_amd64"}, {"Darwin", "arm64", "darwin_arm64"}} {
		t.Run(target.name, func(t *testing.T) {
			f := newInstaller(t)
			f.system, f.arch = target.os, target.arch
			f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
			out, err := f.run(false)
			if err != nil {
				t.Fatal(out, err)
			}
			f.installed("", "v1.0.0")
			if !strings.Contains(f.requests(), "v1.0.0_"+target.name+".tar.gz") || strings.Contains(f.requests(), "_source.tar.gz") {
				t.Fatal(f.requests())
			}
		})
	}
}
func TestInstallerReleaseSelection(t *testing.T) {
	for _, kind := range []string{"stable", "pre", "explicit", "draft", "unavailable", "none", "invalid-json", "pagination"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstaller(t)
			args := []string{}
			want := "v1.0.0"
			switch kind {
			case "stable":
				f.add("v1.0.0", false, false, "2026-01-02T00:00:00Z")
				f.add("v2.0.0-alpha.1", true, false, "2026-02-01T00:00:00Z")
				f.add("v0.9.0", false, false, "2026-01-01T00:00:00Z")
				f.add("v9.0.0", false, true, "2026-12-01T00:00:00Z")
			case "pre":
				f.add("v1.0.0-alpha.1", true, false, "2026-01-01T00:00:00Z")
				f.add("v1.0.0-alpha.2", true, false, "2026-02-01T00:00:00Z")
				want = "v1.0.0-alpha.2"
			case "explicit":
				f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
				f.add("v0.5.0-alpha.1", true, false, "2025-01-01T00:00:00Z")
				want = "v0.5.0-alpha.1"
				args = []string{"--version", want}
			case "draft":
				f.add("v1.0.0", false, true, "2026-01-01T00:00:00Z")
				args = []string{"--version", "v1.0.0"}
			case "unavailable":
				f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
				args = []string{"--version", "v7.0.0"}
			case "none":
				put(t, f.root+"/releases.json", []byte("[]"), 0600)
			case "invalid-json":
				put(t, f.root+"/releases.json", []byte(`[{"tag_name":"v1.0.0",broken]`), 0600)
			case "pagination":
				f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
				one := f.releases[0]
				one.Draft = true
				page := make([]release, 100)
				for i := range page {
					page[i] = one
				}
				raw, _ := json.Marshal(page)
				put(t, f.root+"/releases.json", raw, 0600)
				raw, _ = json.Marshal(f.releases)
				put(t, f.root+"/page2.json", raw, 0600)
			}
			out, err := f.run(true, args...)
			failure := kind == "draft" || kind == "unavailable" || kind == "none" || kind == "invalid-json"
			if failure {
				if err == nil {
					t.Fatal("unexpected success", out)
				}
				if _, e := os.Stat(f.home + "/.local/bin/git-ez"); !os.IsNotExist(e) {
					t.Fatal("installed on failure", e)
				}
				return
			}
			if err != nil {
				t.Fatal(out, err)
			}
			f.installed("", want)
			if kind == "pre" && !strings.Contains(out, "Warning: no stable release") {
				t.Fatal(out)
			}
			if kind == "explicit" && strings.Contains(f.requests(), "per_page") {
				t.Fatal("explicit version substituted", f.requests())
			}
		})
	}
}

func (f *installerFixture) replaceArchive(tag string, data []byte) {
	f.t.Helper()
	name := "git-ez_" + tag + "_linux_amd64.tar.gz"
	put(f.t, f.root+"/"+tag+"/"+name, data, 0600)
	hash := sha256.Sum256(data)
	put(f.t, f.root+"/"+tag+"/SHA256SUMS", []byte(hex.EncodeToString(hash[:])+"  "+name+"\n"), 0600)
}
func TestInstallerFailuresPreserveExistingInstallation(t *testing.T) {
	for _, kind := range []string{"download", "missing-asset", "missing-source", "duplicate-asset", "checksum", "missing-checksum", "ambiguous-checksum", "invalid-archive", "traversal", "absolute-path", "symlink", "hardlink", "duplicate-path", "missing-manual", "missing-binary", "missing-license", "wrong-version", "manual-directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstaller(t)
			f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
			out, err := f.run(false)
			if err != nil {
				t.Fatal(out, err)
			}
			f.add("v2.0.0", false, false, "2026-02-01T00:00:00Z")
			tag := "v2.0.0"
			asset := "git-ez_" + tag + "_linux_amd64.tar.gz"
			dir := f.root + "/" + tag
			switch kind {
			case "download":
				if err = os.Remove(dir + "/" + asset); err != nil {
					t.Fatal(err)
				}
			case "missing-asset":
				f.releases[1].Assets = nil
				f.metadata()
			case "missing-source":
				assets := f.releases[1].Assets
				f.releases[1].Assets = assets[:len(assets)-1]
				f.metadata()
			case "duplicate-asset":
				f.releases[1].Assets = append(f.releases[1].Assets, map[string]string{"name": asset})
				f.metadata()
			case "checksum":
				put(t, dir+"/SHA256SUMS", []byte(strings.Repeat("0", 64)+"  "+asset+"\n"), 0600)
			case "missing-checksum":
				put(t, dir+"/SHA256SUMS", []byte(""), 0600)
			case "ambiguous-checksum":
				raw, err := os.ReadFile(dir + "/SHA256SUMS")
				if err != nil {
					t.Fatal(err)
				}
				put(t, dir+"/SHA256SUMS", append(raw, raw...), 0600)
			case "invalid-archive":
				f.replaceArchive(tag, []byte("not a gzip archive"))
			case "manual-directory":
				manual := f.home + "/.local/share/man/man1/git-ez.1"
				if err := os.Remove(manual); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(manual, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				name := "git-ez_" + tag + "_linux_amd64"
				files := map[string]string{"git-ez": "#!/bin/sh\nprintf '%s\\n' 'git-ez " + tag + "'\n", "docs/man/git-ez.1": "manual\n", "LICENSE": "license", "NOTICE": "notice", "SOURCE.txt": "source", "BUILD.json": "{}", "SBOM.cdx.json": "{}", "DEPENDENCIES.json": "[]", "docs/installation.md": "docs"}
				var extra *tar.Header
				switch kind {
				case "traversal":
					extra = &tar.Header{Name: name + "/../../escaped", Typeflag: tar.TypeReg, Mode: 0600}
				case "absolute-path":
					extra = &tar.Header{Name: f.root + "/escaped", Typeflag: tar.TypeReg, Mode: 0600}
				case "symlink":
					extra = &tar.Header{Name: name + "/link", Linkname: f.root, Typeflag: tar.TypeSymlink, Mode: 0777}
				case "hardlink":
					extra = &tar.Header{Name: name + "/link", Linkname: name + "/git-ez", Typeflag: tar.TypeLink, Mode: 0777}
				case "duplicate-path":
					extra = &tar.Header{Name: name + "/git-ez", Typeflag: tar.TypeReg, Mode: 0700}
				case "missing-manual":
					delete(files, "docs/man/git-ez.1")
				case "missing-binary":
					delete(files, "git-ez")
				case "missing-license":
					delete(files, "LICENSE")
				case "wrong-version":
					files["git-ez"] = "#!/bin/sh\nprintf '%s\\n' 'git-ez v9.0.0'\n"
				}
				f.replaceArchive(tag, testArchive(t, name, files, extra))
			}
			before, err := os.ReadFile(f.home + "/.local/bin/git-ez")
			if err != nil {
				t.Fatal(err)
			}
			out, err = f.run(true)
			if err == nil {
				t.Fatal("unsafe success", out)
			}
			after, err := os.ReadFile(f.home + "/.local/bin/git-ez")
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("existing executable changed", out, err)
			}
			if _, err := os.Stat(f.root + "/escaped"); !os.IsNotExist(err) {
				t.Fatal("archive escaped extraction root", err)
			}
		})
	}
}
func TestInstallerPrefixesUpgradeAndDowngrade(t *testing.T) {
	f := newInstaller(t)
	f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
	prefix := f.root + "/custom prefix"
	put(t, f.home+"/.gitconfig", []byte("user settings sentinel\n"), 0600)
	put(t, f.home+"/audit-sentinel", []byte("audit sentinel\n"), 0600)
	for _, tag := range []string{"v1.0.0", "v2.0.0", "v1.0.0"} {
		if tag == "v2.0.0" {
			f.add(tag, false, false, "2026-02-01T00:00:00Z")
		}
		args := []string{"--prefix", prefix}
		if tag != "v2.0.0" {
			args = append(args, "--version", tag)
		}
		out, err := f.run(true, args...)
		if err != nil {
			t.Fatal(out, err)
		}
		f.installed(prefix, tag)
	}
	for name, want := range map[string]string{".gitconfig": "user settings sentinel\n", "audit-sentinel": "audit sentinel\n"} {
		data, err := os.ReadFile(f.home + "/" + name)
		if err != nil || string(data) != want {
			t.Fatal("user data changed", name, err)
		}
	}
	if _, err := os.Stat(f.home + "/.local"); !os.IsNotExist(err) {
		t.Fatal("custom prefix modified default", err)
	}
}
func TestInstallerRefusesUnrelatedExecutable(t *testing.T) {
	for _, kind := range []string{"unrelated", "symlink", "directory", "broken"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstaller(t)
			f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
			bin := f.home + "/.local/bin"
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			dest := bin + "/git-ez"
			switch kind {
			case "symlink":
				put(t, f.root+"/target", []byte("#!/bin/sh\necho git-ez v0.0.1\n"), 0700)
				if err := os.Symlink(f.root+"/target", dest); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(dest, 0700); err != nil {
					t.Fatal(err)
				}
			case "broken":
				put(t, dest, []byte("#!/bin/sh\nexit 1\n"), 0700)
			default:
				put(t, dest, []byte("#!/bin/sh\necho unrelated tool\n"), 0700)
			}
			info, _ := os.Lstat(dest)
			before, _ := os.ReadFile(dest)
			out, err := f.run(false)
			if err == nil {
				t.Fatal(out)
			}
			afterInfo, err := os.Lstat(dest)
			after, _ := os.ReadFile(dest)
			if err != nil || afterInfo.Mode() != info.Mode() || !bytes.Equal(before, after) {
				t.Fatal("unrelated destination modified", out, err)
			}
		})
	}
}
func TestInstallerPrerequisitesAndArguments(t *testing.T) {
	for _, kind := range []string{"unsupported-os", "unsupported-cpu", "old-git", "invalid-git", "missing-version", "missing-prefix", "invalid-version", "unknown-option", "help"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstaller(t)
			args := []string{}
			switch kind {
			case "unsupported-os":
				f.system = "Windows"
			case "unsupported-cpu":
				f.arch = "riscv64"
			case "old-git":
				f.git = "2.45.9"
			case "invalid-git":
				f.git = "invalid"
			case "missing-version":
				args = []string{"--version"}
			case "missing-prefix":
				args = []string{"--prefix"}
			case "invalid-version":
				args = []string{"--version", "../../bad"}
			case "unknown-option":
				args = []string{"--force"}
			case "help":
				args = []string{"--help"}
			}
			out, err := f.run(true, args...)
			if (kind == "help") != (err == nil) {
				t.Fatal(out, err)
			}
			if f.requests() != "" {
				t.Fatal("prerequisite failure contacted release API", f.requests())
			}
		})
	}
}
func TestInstallerTruncatedAndInterruptedInput(t *testing.T) {
	f := newInstaller(t)
	f.add("v1.0.0", false, false, "2026-01-01T00:00:00Z")
	data, err := os.ReadFile(f.script)
	if err != nil {
		t.Fatal(err)
	}
	final := bytes.LastIndex(data, []byte("install_main \"$@\""))
	if final < 0 {
		t.Fatal("missing final entry point")
	}
	// Every truncation of the final invocation and representative body truncations
	// must fail without starting transport or creating an installation.
	cuts := []int{1, len(data) / 3, len(data) / 2, final}
	for i := final + 1; i < len(data)-2; i++ {
		cuts = append(cuts, i)
	}
	for _, cut := range cuts {
		cmd := exec.Command("sh", "-s")
		cmd.Stdin = bytes.NewReader(data[:cut])
		cmd.Env = f.env()
		out, err := cmd.CombinedOutput()
		_ = out
		_ = err // A comment-only/no-op prefix may exit zero; it must never install.
		if f.requests() != "" {
			t.Fatalf("truncation started installation at %d", cut)
		}
	}
	cmd := exec.Command("sh", f.script)
	cmd.Env = append(f.env(), "INSTALL_TEST_INTERRUPT=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("interruption ignored", string(out))
	}
	entries, err := os.ReadDir(f.temporary)
	if err != nil || len(entries) != 0 {
		t.Fatal("interrupted download leaked temporary files", entries, err)
	}
	if _, err := os.Stat(f.home + "/.local/bin/git-ez"); !os.IsNotExist(err) {
		t.Fatal("installed after interruption", err)
	}
}

// Explicit native qualification after a real local build. Normal tests do not
// build another program or require release assets, historical evidence or network.
func TestInstallerNativeBinary(t *testing.T) {
	binary := os.Getenv("GIT_EZ_INSTALL_TEST_BINARY")
	if binary == "" {
		t.Skip("opt-in: supply a native binary built with main.version=v0.0.0-installer-test")
	}
	f := newInstaller(t)
	f.system = map[string]string{"linux": "Linux", "freebsd": "FreeBSD", "darwin": "Darwin"}[runtime.GOOS]
	f.arch = runtime.GOARCH
	const tag = "v0.0.0-installer-test"
	f.add(tag, true, false, "2026-01-01T00:00:00Z")
	target := runtime.GOOS + "_" + runtime.GOARCH
	name := "git-ez_" + tag + "_" + target
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	manual, err := os.ReadFile("../docs/man/git-ez.1")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"git-ez": string(raw), "docs/man/git-ez.1": string(manual), "LICENSE": "fixture GPL-3.0-only", "NOTICE": "fixture", "SOURCE.txt": "fixture corresponding source", "BUILD.json": "{}", "SBOM.cdx.json": "{}", "DEPENDENCIES.json": "[]", "docs/installation.md": "fixture"}
	data := testArchive(t, name, files, nil)
	put(t, f.root+"/"+tag+"/"+name+".tar.gz", data, 0600)
	hash := sha256.Sum256(data)
	put(t, f.root+"/"+tag+"/SHA256SUMS", []byte(fmt.Sprintf("%x  %s.tar.gz\n", hash, name)), 0600)
	out, err := f.run(true)
	if err != nil {
		t.Fatal(out, err)
	}
	f.installed("", tag)
	prefix := f.home + "/.local"
	nativeEnv := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") && !strings.HasPrefix(entry, "HOME=") {
			nativeEnv = append(nativeEnv, entry)
		}
	}
	nativeEnv = append(nativeEnv, "HOME="+f.home, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	cmd := exec.Command("git", "ez", "--version")
	cmd.Env = append(nativeEnv, "PATH="+prefix+"/bin"+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "git-ez "+tag+"\n" {
		t.Fatal(string(output), err)
	}
	cmd = exec.Command("git", "-c", "help.format=man", "ez", "--help")
	cmd.Env = append(nativeEnv, "PATH="+prefix+"/bin"+string(os.PathListSeparator)+os.Getenv("PATH"), "MANPATH="+prefix+"/share/man", "MANPAGER=cat", "GIT_PAGER=cat", "PAGER=cat")
	output, err = cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "AI-assisted native Git integration") {
		t.Fatal(string(output), err)
	}
}
