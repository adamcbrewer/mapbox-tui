package release

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
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
func verifyArtifact(ctx context.Context, dir, version string) (string, error) {
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
	if _, err = io.Copy(hash, contextReader{ctx, file}); err != nil {
		return "", fmt.Errorf("hash archive: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), fields[0]) {
		return "", fmt.Errorf("checksum mismatch; downloaded binary will not be executed")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	compressed := bufio.NewReader(contextReader{ctx, file})
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return "", fmt.Errorf("read gzip: %w", err)
	}
	defer gz.Close()
	gz.Multistream(false)
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
	var extracted int64
	allowed := map[string]bool{"mapbox-tui": false, "INSTALL.md": false, "LICENSE": false, "THIRD_PARTY_NOTICES.txt": false}
	reader := tar.NewReader(io.LimitReader(contextReader{ctx, gz}, (256<<20)+(64<<10)))
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
		if header.Name != "mapbox-tui" && header.Size > 1<<20 {
			return "", fmt.Errorf("documentation entry %s exceeds 1 MiB", header.Name)
		}
		if header.Size > (256<<20)-extracted {
			return "", fmt.Errorf("total extracted archive size exceeds 256 MiB")
		}
		extracted += header.Size
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
	// GNU tar pads its final 10 KiB record. Bound this read, check the first
	// gzip member's CRC, and reject appended members without decompressing them.
	const maxPadding = 10 * 1024
	padding, err := io.ReadAll(io.LimitReader(gz, maxPadding+1))
	if err != nil {
		return "", fmt.Errorf("gzip integrity: %w", err)
	}
	if len(padding) > maxPadding || strings.Trim(string(padding), "\x00") != "" {
		return "", fmt.Errorf("unexpected data after tar terminator")
	}
	if _, err := compressed.Peek(1); err != io.EOF {
		return "", fmt.Errorf("unexpected data after gzip member")
	}
	ok = true
	return filepath.Join(out, "mapbox-tui"), nil
}

// artifactDigest authenticates every file and its permissions while ignoring
// timestamps and ownership, which differ across local and CI packaging hosts.
// Call only after verifyArtifact has validated the archive structure and checksum.
func artifactDigest(ctx context.Context, dir, version string) (string, error) {
	file, err := os.Open(filepath.Join(dir, archiveName(version)))
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(contextReader{ctx, file})
	if err != nil {
		return "", err
	}
	defer gz.Close()
	hash := sha256.New()
	reader := tar.NewReader(io.LimitReader(contextReader{ctx, gz}, (256<<20)+(64<<10)))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%q %d %d %d\n", header.Name, header.Typeflag, header.Mode, header.Size)
		if _, err := io.Copy(hash, reader); err != nil {
			return "", fmt.Errorf("hash archive entry: %w", err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// contextReader makes hashing and decompression interruptible between reads.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
