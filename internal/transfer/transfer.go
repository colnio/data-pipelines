// Package transfer implements safe archive handling, hash verification, and
// atomic promotion for the lab-data raw ingest pipeline (architecture §13,
// "Safe archive unpacking", "Two hash layers", "Files first DB last", §5).
//
// It is a pure filesystem package. It depends only on internal/domain, the Go
// standard library, and github.com/klauspost/compress/zstd. No DB, no platform
// import.
package transfer

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/colnio/data-pipelines/internal/domain"
)

// Sentinel errors. Use errors.Is to match them.
var (
	// ErrArchiveHashMismatch is returned by VerifyArchiveSHA256 when the
	// computed digest differs from the expected hex string.
	ErrArchiveHashMismatch = errors.New("archive sha256 mismatch")

	// ErrUnsafeArchive is returned by SafeUnpack when the archive contains a
	// path or entry that violates the safe-unpack policy.
	ErrUnsafeArchive = errors.New("unsafe archive entry")

	// ErrManifestMismatch is returned by VerifyAgainstManifest when the
	// extracted file set disagrees with the manifest.
	ErrManifestMismatch = errors.New("manifest mismatch")

	// ErrAlreadyPromoted is returned by Promote when rawRunDir already exists.
	// Callers should treat this as idempotent success.
	ErrAlreadyPromoted = errors.New("run already promoted")
)

// UnpackOptions controls the bomb-prevention limits for SafeUnpack.
type UnpackOptions struct {
	// MaxTotalBytes is the maximum sum of all uncompressed file bytes.
	// Zero means no limit.
	MaxTotalBytes int64
	// MaxFileBytes is the maximum size of a single uncompressed file.
	// Zero means no limit.
	MaxFileBytes int64
	// MaxEntries is the maximum number of tar entries (dirs + files).
	// Zero means no limit.
	MaxEntries int
}

// ExtractedFile records the result of safely extracting one file from an archive.
type ExtractedFile struct {
	// RelPath is the file path relative to destDir, using forward slashes.
	RelPath string
	// Bytes is the number of bytes written.
	Bytes int64
	// SHA256 is the lowercase hex SHA-256 of the file contents.
	SHA256 string
}

// VerifyArchiveSHA256 streams archivePath through SHA-256 and compares the
// digest to expectedHex (case-insensitive). On mismatch it returns a wrapped
// ErrArchiveHashMismatch so callers can map to the manifest_mismatch /
// transfer_failed state.
func VerifyArchiveSHA256(archivePath, expectedHex string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive for hash: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expectedHex) {
		return fmt.Errorf("%w: got %s, want %s", ErrArchiveHashMismatch, got, strings.ToLower(expectedHex))
	}
	return nil
}

// SafeUnpack extracts archivePath into destDir using the safe-unpack policy
// defined in architecture §13.
//
// Compression is detected from the file extension:
//   - .tar.zst or .zst   → zstd (klauspost)
//   - .tar.gz or .tgz    → gzip
//   - .tar               → uncompressed
//
// All dangerous entry types are rejected with a wrapped ErrUnsafeArchive.
// Bomb-prevention limits from opts are enforced against uncompressed bytes.
// Directories are created at mode 0755; regular files are written at mode 0644.
// The SHA-256 of each regular file is computed while streaming.
func SafeUnpack(archivePath, destDir string, opts UnpackOptions) ([]ExtractedFile, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	tr, cleanup, err := openTarReader(f, archivePath)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Canonicalise destDir so that escape checks are reliable.
	destDir, err = filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("abs destDir: %w", err)
	}

	var (
		results    []ExtractedFile
		seenPaths  = map[string]struct{}{}
		totalBytes int64
		entryCount int
	)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		entryCount++
		if opts.MaxEntries > 0 && entryCount > opts.MaxEntries {
			return nil, fmt.Errorf("%w: entry count %d exceeds limit %d",
				ErrUnsafeArchive, entryCount, opts.MaxEntries)
		}

		// ----------------------------------------------------------------
		// Path safety checks
		// ----------------------------------------------------------------
		rawName := hdr.Name

		if filepath.IsAbs(rawName) {
			return nil, fmt.Errorf("%w: absolute path %q", ErrUnsafeArchive, rawName)
		}

		if containsDotDot(rawName) {
			return nil, fmt.Errorf("%w: path traversal in %q", ErrUnsafeArchive, rawName)
		}

		// Compute the full resolved destination path and verify it stays
		// within destDir.
		cleanRel := filepath.Clean(rawName)
		fullPath := filepath.Join(destDir, cleanRel)
		if !strings.HasPrefix(fullPath+string(filepath.Separator), destDir+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: path %q escapes destDir", ErrUnsafeArchive, rawName)
		}

		// Normalise to forward slashes for the dedup key.
		relFwd := filepath.ToSlash(cleanRel)

		// ----------------------------------------------------------------
		// Entry type checks
		// ----------------------------------------------------------------
		switch hdr.Typeflag {
		case tar.TypeDir:
			// Directories are fine; handled below.
		case tar.TypeReg, tar.TypeRegA:
			// Regular files are fine; handled below.
		case tar.TypeSymlink:
			return nil, fmt.Errorf("%w: symlink %q", ErrUnsafeArchive, rawName)
		case tar.TypeLink:
			return nil, fmt.Errorf("%w: hardlink %q", ErrUnsafeArchive, rawName)
		case tar.TypeBlock:
			return nil, fmt.Errorf("%w: block device %q", ErrUnsafeArchive, rawName)
		case tar.TypeChar:
			return nil, fmt.Errorf("%w: char device %q", ErrUnsafeArchive, rawName)
		case tar.TypeFifo:
			return nil, fmt.Errorf("%w: FIFO %q", ErrUnsafeArchive, rawName)
		default:
			return nil, fmt.Errorf("%w: unsupported entry type %d in %q",
				ErrUnsafeArchive, hdr.Typeflag, rawName)
		}

		// ----------------------------------------------------------------
		// Duplicate path check (applies to both dirs and files)
		// ----------------------------------------------------------------
		if _, dup := seenPaths[relFwd]; dup {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrUnsafeArchive, rawName)
		}
		seenPaths[relFwd] = struct{}{}

		// ----------------------------------------------------------------
		// Handle directory
		// ----------------------------------------------------------------
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				return nil, fmt.Errorf("mkdir %q: %w", fullPath, err)
			}
			continue
		}

		// ----------------------------------------------------------------
		// Handle regular file
		// ----------------------------------------------------------------

		// Per-file size guard using the header size.
		if opts.MaxFileBytes > 0 && hdr.Size > opts.MaxFileBytes {
			return nil, fmt.Errorf("%w: file %q header size %d exceeds per-file limit %d",
				ErrUnsafeArchive, rawName, hdr.Size, opts.MaxFileBytes)
		}

		// Ensure parent directory exists.
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return nil, fmt.Errorf("mkdir parent of %q: %w", fullPath, err)
		}

		written, digest, err := writeFileWithHash(tr, fullPath, opts.MaxFileBytes, opts.MaxTotalBytes-totalBytes)
		if err != nil {
			return nil, fmt.Errorf("write file %q: %w", rawName, err)
		}

		totalBytes += written
		if opts.MaxTotalBytes > 0 && totalBytes > opts.MaxTotalBytes {
			return nil, fmt.Errorf("%w: total uncompressed bytes %d exceeds limit %d",
				ErrUnsafeArchive, totalBytes, opts.MaxTotalBytes)
		}

		results = append(results, ExtractedFile{
			RelPath: relFwd,
			Bytes:   written,
			SHA256:  hex.EncodeToString(digest),
		})
	}

	return results, nil
}

// VerifyAgainstManifest checks that extracted matches the files declared in m.
//
//   - Every ManifestFile must be present, with matching Bytes and SHA256.
//   - If m.InstrumentJSON != "", that file must be present.
//   - If !allowExtra, every extracted file must be listed in the manifest.
//
// Errors are wrapped ErrManifestMismatch with a human-readable reason.
func VerifyAgainstManifest(extracted []ExtractedFile, m domain.Manifest, allowExtra bool) error {
	byPath := make(map[string]ExtractedFile, len(extracted))
	for _, ef := range extracted {
		byPath[ef.RelPath] = ef
	}

	// Check every declared file.
	for _, mf := range m.Files {
		ef, ok := byPath[mf.Name]
		if !ok {
			return fmt.Errorf("%w: declared file %q not found in extracted set", ErrManifestMismatch, mf.Name)
		}
		if ef.Bytes != mf.Bytes {
			return fmt.Errorf("%w: file %q bytes mismatch: got %d, want %d",
				ErrManifestMismatch, mf.Name, ef.Bytes, mf.Bytes)
		}
		if !strings.EqualFold(ef.SHA256, mf.SHA256) {
			return fmt.Errorf("%w: file %q sha256 mismatch: got %s, want %s",
				ErrManifestMismatch, mf.Name, ef.SHA256, strings.ToLower(mf.SHA256))
		}
	}

	// If InstrumentJSON is declared, require it to be present.
	if m.InstrumentJSON != "" {
		if _, ok := byPath[m.InstrumentJSON]; !ok {
			return fmt.Errorf("%w: instrument_json file %q not found in extracted set",
				ErrManifestMismatch, m.InstrumentJSON)
		}
	}

	// Reject extras if not allowed.
	if !allowExtra {
		declared := make(map[string]struct{}, len(m.Files))
		for _, mf := range m.Files {
			declared[mf.Name] = struct{}{}
		}
		for _, ef := range extracted {
			if _, ok := declared[ef.RelPath]; !ok {
				return fmt.Errorf("%w: unexpected file %q not listed in manifest",
					ErrManifestMismatch, ef.RelPath)
			}
		}
	}

	return nil
}

// Promote atomically moves stagingRunDir into rawRunDir ("files first, DB
// last"). It follows the architecture §5 / §13 immutable-store rules:
//
//   - Creates rawRunDir's parent directory if needed.
//   - Returns ErrAlreadyPromoted (errors.Is-able) if rawRunDir already exists.
//   - Otherwise os.Rename(staging→raw) (same filesystem assumed).
//   - Walks the promoted tree and sets files to 0444, dirs to 0555.
//   - Best-effort fsyncs the parent directory.
func Promote(stagingRunDir, rawRunDir string) error {
	rawParent := filepath.Dir(rawRunDir)
	if err := os.MkdirAll(rawParent, 0755); err != nil {
		return fmt.Errorf("create raw parent %q: %w", rawParent, err)
	}

	// Idempotency: if the destination already exists, report already-promoted.
	if _, err := os.Lstat(rawRunDir); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyPromoted, rawRunDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat rawRunDir %q: %w", rawRunDir, err)
	}

	// Atomic move (requires same filesystem).
	if err := os.Rename(stagingRunDir, rawRunDir); err != nil {
		return fmt.Errorf("promote rename %q → %q: %w", stagingRunDir, rawRunDir, err)
	}

	// Make the tree immutable to normal users.
	if err := lockDown(rawRunDir); err != nil {
		// Non-fatal but log-worthy; the data is safely promoted.
		_ = err
	}

	// Best-effort fsync of parent directory.
	_ = fsyncDir(rawParent)

	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// openTarReader wraps r in the appropriate decompressor based on archivePath's
// extension, and returns a *tar.Reader and an optional cleanup function.
func openTarReader(r io.Reader, archivePath string) (*tar.Reader, func(), error) {
	lower := strings.ToLower(archivePath)

	switch {
	case strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".zst"):
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("open zstd reader: %w", err)
		}
		return tar.NewReader(zr), func() { zr.Close() }, nil

	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		gr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("open gzip reader: %w", err)
		}
		return tar.NewReader(gr), func() { _ = gr.Close() }, nil

	case strings.HasSuffix(lower, ".tar"):
		return tar.NewReader(r), nil, nil

	default:
		return nil, nil, fmt.Errorf("unrecognised archive extension: %q", archivePath)
	}
}

// containsDotDot reports whether any path component is "..".
func containsDotDot(p string) bool {
	// Normalise separators so we catch both / and \ on any OS.
	p = filepath.ToSlash(p)
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// writeFileWithHash creates path, streams from r into it while computing
// SHA-256, and returns the number of bytes written and the digest.
//
// remainingTotal, if > 0, is used as an additional cap on what will be read
// (acts as a secondary guard against a lying header).
func writeFileWithHash(r io.Reader, path string, maxFile, remainingTotal int64) (int64, []byte, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, nil, err
	}
	defer out.Close()

	h := sha256.New()
	w := io.MultiWriter(out, h)

	// Build a limited reader to guard against a lying header.
	var lr io.Reader = r
	if maxFile > 0 && remainingTotal > 0 {
		cap := maxFile
		if remainingTotal < cap {
			cap = remainingTotal
		}
		// We read one extra byte to detect if the stream exceeds the limit.
		lr = io.LimitReader(r, cap+1)
	} else if maxFile > 0 {
		lr = io.LimitReader(r, maxFile+1)
	} else if remainingTotal > 0 {
		lr = io.LimitReader(r, remainingTotal+1)
	}

	n, err := io.Copy(w, lr)
	if err != nil {
		return 0, nil, err
	}

	// Check if we hit the cap (meaning the actual data exceeded the limit).
	if maxFile > 0 && n > maxFile {
		return 0, nil, fmt.Errorf("%w: file exceeded per-file byte limit of %d", ErrUnsafeArchive, maxFile)
	}
	if remainingTotal > 0 && n > remainingTotal {
		return 0, nil, fmt.Errorf("%w: total byte limit would be exceeded", ErrUnsafeArchive)
	}

	return n, h.Sum(nil), nil
}

// lockDown walks root and sets files to 0444, dirs to 0555.
func lockDown(root string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(p, 0555)
		}
		return os.Chmod(p, 0444)
	})
}

// fsyncDir opens dir and calls Sync() on it (best-effort; ignored on
// platforms where this is not supported).
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
