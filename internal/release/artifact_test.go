package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeArtifactFixture(t, dir, artifactFixture(t, artifactHeaders()), "")
	binary, err := verifyArtifact(dir, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(binary)
	if err != nil || string(content) != "executable fixture" {
		t.Fatalf("extracted binary = %q, %v", content, err)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("binary permissions = %v", info.Mode())
	}
	second, err := verifyArtifact(dir, "v0.2.0")
	if err != nil || second == binary {
		t.Fatalf("repeat verification must extract afresh: %q, %v", second, err)
	}
}

func TestVerifyArtifactRejectsUnsafeArchive(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func([]tar.Header) []tar.Header
	}{
		{name: "traversal", mutate: func(headers []tar.Header) []tar.Header {
			headers[0].Name = "../mapbox-tui"
			return headers
		}},
		{name: "absolute path", mutate: func(headers []tar.Header) []tar.Header {
			headers[0].Name = "/mapbox-tui"
			return headers
		}},
		{name: "symlink", mutate: func(headers []tar.Header) []tar.Header {
			headers[0].Typeflag, headers[0].Size, headers[0].Linkname = tar.TypeSymlink, 0, "../target"
			return headers
		}},
		{name: "hard link", mutate: func(headers []tar.Header) []tar.Header {
			headers[0].Typeflag, headers[0].Size, headers[0].Linkname = tar.TypeLink, 0, "../target"
			return headers
		}},
		{name: "duplicate", mutate: func(headers []tar.Header) []tar.Header {
			return append(headers, headers[0])
		}},
		{name: "missing license", mutate: func(headers []tar.Header) []tar.Header {
			return headers[:len(headers)-1]
		}},
		{name: "unexpected file", mutate: func(headers []tar.Header) []tar.Header {
			headers[0].Name = "other-executable"
			return headers
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeArtifactFixture(t, dir, artifactFixture(t, tc.mutate(artifactHeaders())), "")
			assertArtifactRejected(t, dir)
		})
	}
}

func TestVerifyArtifactRejectsCorruption(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		manifest string
		mutate   func([]byte) []byte
	}{
		{name: "checksum mismatch", manifest: strings.Repeat("0", 64) + "  " + archiveName("v0.2.0")},
		{name: "other filename", manifest: strings.Repeat("0", 64) + "  unexpected.tar.gz"},
		{name: "multiple checksums", manifest: strings.Repeat("0", 64) + "  " + archiveName("v0.2.0") + "\nextra"},
		{name: "invalid digest", manifest: "not-a-hash  " + archiveName("v0.2.0")},
		{name: "gzip CRC", mutate: func(data []byte) []byte {
			data[len(data)-8] ^= 0xff
			return data
		}},
		{name: "truncated gzip", mutate: func(data []byte) []byte {
			return data[:len(data)-4]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			data := artifactFixture(t, artifactHeaders())
			if tc.mutate != nil {
				data = tc.mutate(data)
			}
			writeArtifactFixture(t, dir, data, tc.manifest)
			assertArtifactRejected(t, dir)
		})
	}
}

func TestVerifyArtifactRejectsInvalidSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		size int64
	}{
		{name: "oversized member", size: (256 << 20) + 1},
		{name: "truncated member", size: 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var raw bytes.Buffer
			writer := tar.NewWriter(&raw)
			header := tar.Header{Name: "mapbox-tui", Typeflag: tar.TypeReg, Size: tc.size, Mode: 0o755}
			if err := writer.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			// Deliberately omit the body, without allocating the declared member size.
			if err := writer.Close(); err == nil {
				t.Fatal("fixture unexpectedly contains the complete member")
			}
			var compressed bytes.Buffer
			gz := gzip.NewWriter(&compressed)
			if _, err := gz.Write(raw.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			writeArtifactFixture(t, dir, compressed.Bytes(), "")
			assertArtifactRejected(t, dir)
		})
	}
}

func artifactHeaders() []tar.Header {
	return []tar.Header{
		{Name: "mapbox-tui", Typeflag: tar.TypeReg, Size: int64(len("executable fixture")), Mode: 0o755},
		{Name: "INSTALL.md", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "THIRD_PARTY_NOTICES.txt", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "LICENSE", Typeflag: tar.TypeReg, Mode: 0o644},
	}
}

func artifactFixture(t *testing.T, headers []tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for _, header := range headers {
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := writer.Write([]byte("executable fixture")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeArtifactFixture(t *testing.T, dir string, data []byte, manifest string) {
	t.Helper()
	name := archiveName("v0.2.0")
	if manifest == "" {
		manifest = fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertArtifactRejected(t *testing.T, dir string) {
	t.Helper()
	binary, err := verifyArtifact(dir, "v0.2.0")
	if err == nil || binary != "" {
		t.Fatalf("unsafe artifact accepted: %q, %v", binary, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "verified-") {
			t.Fatalf("rejected extraction left executable files in %s", entry.Name())
		}
	}
}
