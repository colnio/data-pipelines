package transfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/domain"
)

// ---------------------------------------------------------------------------
// Archive builder helpers
// ---------------------------------------------------------------------------

type tarEntry struct {
	name     string
	typeflag byte
	content  []byte
	linkname string // for symlinks / hardlinks
}

// buildTar constructs a raw tar into a *bytes.Buffer.
func buildTar(entries []tarEntry) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Linkname: e.linkname,
		}
		switch e.typeflag {
		case tar.TypeDir:
			hdr.Mode = 0755
			hdr.Size = 0
		case tar.TypeReg, tar.TypeRegA:
			hdr.Mode = 0644
			hdr.Size = int64(len(e.content))
		default:
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeTarFile writes a plain .tar file to a temp file path and returns the path.
func writeTarFile(t *testing.T, entries []tarEntry) string {
	t.Helper()
	buf, err := buildTar(entries)
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "test.tar")
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0644))
	return p
}

// writeTarZstFile writes a .tar.zst file to a temp file path and returns the path.
func writeTarZstFile(t *testing.T, entries []tarEntry) string {
	t.Helper()
	tarBuf, err := buildTar(entries)
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	zw, err := zstd.NewWriter(buf)
	require.NoError(t, err)
	_, err = io.Copy(zw, tarBuf)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	p := filepath.Join(t.TempDir(), "test.tar.zst")
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0644))
	return p
}

// writeTarGzFile writes a .tar.gz file.
func writeTarGzFile(t *testing.T, entries []tarEntry) string {
	t.Helper()
	tarBuf, err := buildTar(entries)
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	_, err = io.Copy(gw, tarBuf)
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	p := filepath.Join(t.TempDir(), "test.tar.gz")
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0644))
	return p
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// readFileBytes reads a file and returns its contents.
func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

// ---------------------------------------------------------------------------
// VerifyArchiveSHA256 tests
// ---------------------------------------------------------------------------

func TestVerifyArchiveSHA256_Match(t *testing.T) {
	data := []byte("hello archive")
	p := filepath.Join(t.TempDir(), "archive.tar")
	require.NoError(t, os.WriteFile(p, data, 0644))

	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])
	require.NoError(t, VerifyArchiveSHA256(p, expected))
}

func TestVerifyArchiveSHA256_UppercaseMatch(t *testing.T) {
	data := []byte("hello archive")
	p := filepath.Join(t.TempDir(), "archive.tar")
	require.NoError(t, os.WriteFile(p, data, 0644))

	h := sha256.Sum256(data)
	// Use uppercase hex — should still match case-insensitively.
	expected := hex.EncodeToString(h[:])
	upper := bytes.ToUpper([]byte(expected))
	require.NoError(t, VerifyArchiveSHA256(p, string(upper)))
}

func TestVerifyArchiveSHA256_Mismatch(t *testing.T) {
	data := []byte("hello archive")
	p := filepath.Join(t.TempDir(), "archive.tar")
	require.NoError(t, os.WriteFile(p, data, 0644))

	err := VerifyArchiveSHA256(p, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrArchiveHashMismatch)
}

func TestVerifyArchiveSHA256_FileNotFound(t *testing.T) {
	err := VerifyArchiveSHA256("/nonexistent/archive.tar", "abc123")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// SafeUnpack negative tests — each must return an error and write nothing
// ---------------------------------------------------------------------------

func TestSafeUnpack_PathTraversal(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "../escape", typeflag: tar.TypeReg, content: []byte("evil")},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err, "expected error for path traversal")
	assert.ErrorIs(t, err, ErrUnsafeArchive)
	// Nothing must have been written outside dest.
	escapedPath := filepath.Join(filepath.Dir(dest), "escape")
	_, statErr := os.Lstat(escapedPath)
	assert.True(t, os.IsNotExist(statErr), "escaped file must not exist")
}

func TestSafeUnpack_AbsolutePath(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "/etc/passwd", typeflag: tar.TypeReg, content: []byte("evil")},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_Symlink(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_Hardlink(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "hlink", typeflag: tar.TypeLink, linkname: "some/other/file"},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_FIFO(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "myfifo", typeflag: tar.TypeFifo},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_BlockDevice(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "sda", typeflag: tar.TypeBlock},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_CharDevice(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "tty0", typeflag: tar.TypeChar},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_DuplicatePath(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "data.txt", typeflag: tar.TypeReg, content: []byte("first")},
		{name: "data.txt", typeflag: tar.TypeReg, content: []byte("second")},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_TotalBytesOverLimit(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 100)
	archivePath := writeTarFile(t, []tarEntry{
		{name: "a.dat", typeflag: tar.TypeReg, content: content},
		{name: "b.dat", typeflag: tar.TypeReg, content: content},
	})
	dest := t.TempDir()
	// MaxTotalBytes set to less than 200.
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{MaxTotalBytes: 150})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_SingleFileOverLimit(t *testing.T) {
	content := bytes.Repeat([]byte("y"), 100)
	archivePath := writeTarFile(t, []tarEntry{
		{name: "big.dat", typeflag: tar.TypeReg, content: content},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{MaxFileBytes: 50})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

func TestSafeUnpack_EntryCountOverLimit(t *testing.T) {
	archivePath := writeTarFile(t, []tarEntry{
		{name: "a.dat", typeflag: tar.TypeReg, content: []byte("1")},
		{name: "b.dat", typeflag: tar.TypeReg, content: []byte("2")},
		{name: "c.dat", typeflag: tar.TypeReg, content: []byte("3")},
	})
	dest := t.TempDir()
	_, err := SafeUnpack(archivePath, dest, UnpackOptions{MaxEntries: 2})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeArchive)
}

// ---------------------------------------------------------------------------
// SafeUnpack happy-path tests
// ---------------------------------------------------------------------------

func TestSafeUnpack_PlainTar(t *testing.T) {
	fileA := []byte("content of file A")
	fileB := []byte("content of file B, slightly longer")

	archivePath := writeTarFile(t, []tarEntry{
		{name: "subdir/", typeflag: tar.TypeDir},
		{name: "subdir/a.dat", typeflag: tar.TypeReg, content: fileA},
		{name: "b.dat", typeflag: tar.TypeReg, content: fileB},
	})
	dest := t.TempDir()

	results, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.NoError(t, err)
	require.Len(t, results, 2, "only regular files in results")

	byPath := make(map[string]ExtractedFile)
	for _, ef := range results {
		byPath[ef.RelPath] = ef
	}

	efA, ok := byPath["subdir/a.dat"]
	require.True(t, ok)
	assert.Equal(t, int64(len(fileA)), efA.Bytes)
	assert.Equal(t, sha256Hex(fileA), efA.SHA256)

	efB, ok := byPath["b.dat"]
	require.True(t, ok)
	assert.Equal(t, int64(len(fileB)), efB.Bytes)
	assert.Equal(t, sha256Hex(fileB), efB.SHA256)

	// Verify actual on-disk file contents and hash.
	gotA := readFileBytes(t, filepath.Join(dest, "subdir", "a.dat"))
	assert.Equal(t, fileA, gotA)
	gotB := readFileBytes(t, filepath.Join(dest, "b.dat"))
	assert.Equal(t, fileB, gotB)
}

func TestSafeUnpack_TarZst(t *testing.T) {
	fileC := []byte("zstd compressed content here")

	archivePath := writeTarZstFile(t, []tarEntry{
		{name: "c.dat", typeflag: tar.TypeReg, content: fileC},
	})
	dest := t.TempDir()

	results, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)

	ef := results[0]
	assert.Equal(t, "c.dat", ef.RelPath)
	assert.Equal(t, int64(len(fileC)), ef.Bytes)
	assert.Equal(t, sha256Hex(fileC), ef.SHA256)

	gotC := readFileBytes(t, filepath.Join(dest, "c.dat"))
	assert.Equal(t, fileC, gotC)
}

func TestSafeUnpack_TarGz(t *testing.T) {
	fileD := []byte("gzip compressed content here")

	archivePath := writeTarGzFile(t, []tarEntry{
		{name: "d.dat", typeflag: tar.TypeReg, content: fileD},
	})
	dest := t.TempDir()

	results, err := SafeUnpack(archivePath, dest, UnpackOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)

	ef := results[0]
	assert.Equal(t, "d.dat", ef.RelPath)
	assert.Equal(t, sha256Hex(fileD), ef.SHA256)
}

// ---------------------------------------------------------------------------
// VerifyAgainstManifest tests
// ---------------------------------------------------------------------------

func makeManifest(files []domain.ManifestFile, instrJSON string) domain.Manifest {
	return domain.Manifest{
		RunID:          "run-001",
		SchemaVersion:  1,
		InstrumentJSON: instrJSON,
		Files:          files,
	}
}

func TestVerifyAgainstManifest_AllMatch(t *testing.T) {
	data := []byte("test content")
	m := makeManifest([]domain.ManifestFile{
		{Name: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}, "")
	extracted := []ExtractedFile{
		{RelPath: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}
	require.NoError(t, VerifyAgainstManifest(extracted, m, false))
}

func TestVerifyAgainstManifest_SHA256CaseInsensitive(t *testing.T) {
	data := []byte("test content")
	lower := sha256Hex(data)
	upper := bytes.ToUpper([]byte(lower))
	m := makeManifest([]domain.ManifestFile{
		{Name: "data.csv", Bytes: int64(len(data)), SHA256: string(upper)},
	}, "")
	extracted := []ExtractedFile{
		{RelPath: "data.csv", Bytes: int64(len(data)), SHA256: lower},
	}
	require.NoError(t, VerifyAgainstManifest(extracted, m, false))
}

func TestVerifyAgainstManifest_MissingFile(t *testing.T) {
	data := []byte("test content")
	m := makeManifest([]domain.ManifestFile{
		{Name: "missing.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}, "")
	extracted := []ExtractedFile{}
	err := VerifyAgainstManifest(extracted, m, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrManifestMismatch)
}

func TestVerifyAgainstManifest_ByteMismatch(t *testing.T) {
	data := []byte("test content")
	m := makeManifest([]domain.ManifestFile{
		{Name: "data.csv", Bytes: 9999, SHA256: sha256Hex(data)},
	}, "")
	extracted := []ExtractedFile{
		{RelPath: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}
	err := VerifyAgainstManifest(extracted, m, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrManifestMismatch)
}

func TestVerifyAgainstManifest_SHA256Mismatch(t *testing.T) {
	data := []byte("test content")
	m := makeManifest([]domain.ManifestFile{
		{Name: "data.csv", Bytes: int64(len(data)), SHA256: "badbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbad0"},
	}, "")
	extracted := []ExtractedFile{
		{RelPath: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}
	err := VerifyAgainstManifest(extracted, m, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrManifestMismatch)
}

func TestVerifyAgainstManifest_UnexpectedExtraFile_Rejected(t *testing.T) {
	data := []byte("test content")
	m := makeManifest([]domain.ManifestFile{
		{Name: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}, "")
	extracted := []ExtractedFile{
		{RelPath: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
		{RelPath: "extra.txt", Bytes: 5, SHA256: sha256Hex([]byte("extra"))},
	}
	err := VerifyAgainstManifest(extracted, m, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrManifestMismatch)
}

func TestVerifyAgainstManifest_UnexpectedExtraFile_Allowed(t *testing.T) {
	data := []byte("test content")
	m := makeManifest([]domain.ManifestFile{
		{Name: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}, "")
	extracted := []ExtractedFile{
		{RelPath: "data.csv", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
		{RelPath: "extra.txt", Bytes: 5, SHA256: sha256Hex([]byte("extra"))},
	}
	// allowExtra=true should not return an error.
	require.NoError(t, VerifyAgainstManifest(extracted, m, true))
}

func TestVerifyAgainstManifest_InstrumentJSONRequired(t *testing.T) {
	data := []byte("some json")
	m := makeManifest([]domain.ManifestFile{
		{Name: "params.json", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}, "params.json")
	// InstrumentJSON present in extracted.
	extracted := []ExtractedFile{
		{RelPath: "params.json", Bytes: int64(len(data)), SHA256: sha256Hex(data)},
	}
	require.NoError(t, VerifyAgainstManifest(extracted, m, false))
}

func TestVerifyAgainstManifest_InstrumentJSONMissing(t *testing.T) {
	m := makeManifest([]domain.ManifestFile{}, "params.json")
	extracted := []ExtractedFile{}
	err := VerifyAgainstManifest(extracted, m, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrManifestMismatch)
}

// ---------------------------------------------------------------------------
// Promote tests
// ---------------------------------------------------------------------------

// makeTempPromoteDir creates a temporary directory for promote tests and
// registers a cleanup that first makes the tree writable so that Go's
// TempDir cleanup (which uses os.RemoveAll) can delete it even after lockDown
// has set everything to 0444/0555.
func makeTempPromoteDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "promote-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		// Make writable before removal so RemoveAll can delete 0444/0555 entries.
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return os.Chmod(p, 0755)
			}
			return os.Chmod(p, 0644)
		})
		_ = os.RemoveAll(dir)
	})
	return dir
}

func TestPromote_HappyPath(t *testing.T) {
	staging := makeTempPromoteDir(t)
	rawRoot := makeTempPromoteDir(t)
	rawRunDir := filepath.Join(rawRoot, "run-001")

	// Write some files into staging.
	require.NoError(t, os.MkdirAll(filepath.Join(staging, "sub"), 0755))
	fileContent := []byte("measurement data")
	require.NoError(t, os.WriteFile(filepath.Join(staging, "data.dat"), fileContent, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "sub", "nested.dat"), fileContent, 0644))

	err := Promote(staging, rawRunDir)
	require.NoError(t, err)

	// Files must exist at the new location.
	got := readFileBytes(t, filepath.Join(rawRunDir, "data.dat"))
	assert.Equal(t, fileContent, got)

	// Files must be read-only (0444).
	info, err := os.Stat(filepath.Join(rawRunDir, "data.dat"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0444), info.Mode().Perm())

	// Directories must be 0555.
	dirInfo, err := os.Stat(filepath.Join(rawRunDir, "sub"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0555), dirInfo.Mode().Perm())

	// SHA-256 of promoted file must match original.
	assert.Equal(t, sha256Hex(fileContent), sha256Hex(got))
}

func TestPromote_AlreadyPromoted(t *testing.T) {
	rawRoot := t.TempDir()
	rawRunDir := filepath.Join(rawRoot, "run-already")
	require.NoError(t, os.MkdirAll(rawRunDir, 0755))

	staging := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staging, "x.dat"), []byte("x"), 0644))

	err := Promote(staging, rawRunDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyPromoted)
}

func TestPromote_CreatesParentDir(t *testing.T) {
	base := makeTempPromoteDir(t)
	// Use a deeply nested rawRunDir whose parents do not yet exist.
	rawRunDir := filepath.Join(base, "deep", "nested", "run-001")

	staging := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staging, "file.dat"), []byte("data"), 0644))

	err := Promote(staging, rawRunDir)
	require.NoError(t, err)

	_, err = os.Stat(rawRunDir)
	require.NoError(t, err, "rawRunDir must exist after promote")
}

// ---------------------------------------------------------------------------
// Integration: unpack then verify against manifest
// ---------------------------------------------------------------------------

func TestUnpackAndVerify_EndToEnd(t *testing.T) {
	iv := []byte("iv_sweep data content")
	params := []byte(`{"instrument": "probe_station"}`)

	archivePath := writeTarZstFile(t, []tarEntry{
		{name: "iv_sweep.data", typeflag: tar.TypeReg, content: iv},
		{name: "params_run.json", typeflag: tar.TypeReg, content: params},
	})
	dest := t.TempDir()

	results, err := SafeUnpack(archivePath, dest, UnpackOptions{
		MaxTotalBytes: 10 * 1024 * 1024,
		MaxFileBytes:  5 * 1024 * 1024,
		MaxEntries:    100,
	})
	require.NoError(t, err)

	m := domain.Manifest{
		RunID:          "run-e2e",
		SchemaVersion:  1,
		InstrumentJSON: "params_run.json",
		Files: []domain.ManifestFile{
			{Name: "iv_sweep.data", Bytes: int64(len(iv)), SHA256: sha256Hex(iv)},
			{Name: "params_run.json", Bytes: int64(len(params)), SHA256: sha256Hex(params)},
		},
	}

	err = VerifyAgainstManifest(results, m, false)
	require.NoError(t, err)
}
