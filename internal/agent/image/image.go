// Package image fetches base images onto the agent node and verifies their
// integrity before they are imported into a libvirt storage pool.
package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrChecksumMismatch = errors.New("image checksum mismatch")
	ErrSizeMismatch     = errors.New("image size mismatch")
)

// Fetcher downloads images from HTTP(S), file:// or local paths.
type Fetcher struct {
	client  *http.Client
	workDir string
}

func New(workDir string) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: 30 * time.Minute,
		},
		workDir: workDir,
	}
}

// Result describes a downloaded image.
type Result struct {
	Path string
	Size int64
}

// Fetch downloads the image at sourceURL into the work directory. When the
// source is a local path and already inside the work directory it is used
// directly. Expected size/checksum are verified when provided.
func (f *Fetcher) Fetch(ctx context.Context, sourceURL, expectedChecksum string, expectedSize int64) (*Result, error) {
	if err := os.MkdirAll(f.workDir, 0o755); err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}

	dest := filepath.Join(f.workDir, imageFileName(sourceURL))

	if isLocal(sourceURL) {
		return f.fetchLocal(ctx, sourceURL, dest, expectedChecksum, expectedSize)
	}

	if err := f.download(ctx, sourceURL, dest); err != nil {
		return nil, err
	}

	if err := verify(dest, expectedChecksum, expectedSize); err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	return &Result{Path: dest}, nil
}

func (f *Fetcher) fetchLocal(ctx context.Context, src, dest, checksum string, size int64) (*Result, error) {
	if abs, err := filepath.Abs(src); err == nil && fileExists(abs) {
		src = abs
	} else if !fileExists(src) {
		return nil, fmt.Errorf("image source not found: %s", src)
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	// If it's already in the work dir we can use it in place.
	if filepath.Dir(src) == f.workDir {
		if err := verify(src, checksum, size); err != nil {
			return nil, err
		}
		return &Result{Path: src, Size: info.Size()}, nil
	}
	if err := copyFile(ctx, src, dest); err != nil {
		return nil, err
	}
	if err := verify(dest, checksum, size); err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	return &Result{Path: dest, Size: info.Size()}, nil
}

func (f *Fetcher) download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %d", url, resp.StatusCode)
	}
	out, err := os.Create(dest + ".tmp")
	if err != nil {
		return fmt.Errorf("create temp image: %w", err)
	}
	written, err := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(dest + ".tmp")
		return fmt.Errorf("stream download: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(dest + ".tmp")
		return fmt.Errorf("close temp image: %w", closeErr)
	}
	if err := os.Rename(dest+".tmp", dest); err != nil {
		_ = os.Remove(dest + ".tmp")
		return fmt.Errorf("finalize image: %w", err)
	}
	_ = written
	return nil
}

func verify(path, expectedChecksum string, expectedSize int64) error {
	if expectedChecksum != "" {
		sum, err := sha256File(path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, expectedChecksum) {
			return ErrChecksumMismatch
		}
	}
	if expectedSize > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() != expectedSize {
			return ErrSizeMismatch
		}
	}
	return nil
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

func copyFile(ctx context.Context, src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest + ".tmp")
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dest + ".tmp")
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest + ".tmp")
		return err
	}
	return os.Rename(dest+".tmp", dest)
}

func isLocal(src string) bool {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return false
	}
	if strings.HasPrefix(src, "s3://") || strings.HasPrefix(src, "file://") {
		return true
	}
	return true // bare paths and file:// are local
}

func imageFileName(url string) string {
	name := filepath.Base(strings.TrimSuffix(url, "/"))
	if name == "" || name == "." {
		return "image.img"
	}
	return name
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}