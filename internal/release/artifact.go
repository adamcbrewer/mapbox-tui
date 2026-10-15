package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func archiveName(version string) string { return "mapbox-tui-" + version + "-linux-amd64.tar.gz" }

// verifyArtifact checks the exact checksum manifest before extracting a strict
// allowlist of regular files into a newly-created private directory.
func verifyArtifact(dir, version string) (string, error) {
	name := archiveName(version)
	manifest, err := os.ReadFile(filepath.Join(dir, name+".sha256"))
	if err != nil {
		return "", fmt.Errorf("read checksum: %w", err)
	}
	fields := strings.Fields(string(manifest))
	if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
		return "", fmt.Errorf("checksum manifest must name only %s", name)
	}
	digest, err := hex.DecodeString(fields[0])
	if err != nil || len(digest) != sha256.Size {
		return "", fmt.Errorf("invalid SHA-256 digest")
	}
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash archive: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), fields[0]) {
		return "", fmt.Errorf("checksum mismatch; downloaded binary will not be executed")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("read gzip: %w", err)
	}
	defer gz.Close()
	out, err := os.MkdirTemp(dir, "verified-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(out)
		}
	}()
	allowed := map[string]bool{"mapbox-tui": false, "INSTALL.md": false, "LICENSE": false, "THIRD_PARTY_NOTICES.txt": false}
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}
		seen, exists := allowed[header.Name]
		if !exists || seen || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 256<<20 {
			return "", fmt.Errorf("unexpected or unsafe archive entry %q", header.Name)
		}
		allowed[header.Name] = true
		mode := os.FileMode(0o644)
		if header.Name == "mapbox-tui" {
			mode = 0o755
		}
		dest, err := os.OpenFile(filepath.Join(out, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return "", err
		}
		_, copyErr := io.CopyN(dest, reader, header.Size)
		closeErr := dest.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	for name, present := range allowed {
		if !present {
			return "", fmt.Errorf("archive is missing %s", name)
		}
	}
	// Consume the gzip trailer, including its CRC, before allowing execution.
	if _, err = io.Copy(io.Discard, gz); err != nil {
		return "", fmt.Errorf("gzip integrity: %w", err)
	}
	ok = true
	return filepath.Join(out, "mapbox-tui"), nil
}

// binaryDigest binds remote execution to the independently built local executable.
func binaryDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open binary: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash binary: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
