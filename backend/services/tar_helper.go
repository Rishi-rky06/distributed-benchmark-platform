package services

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractSubmissionZip finds the first .zip in dir, extracts it into dir (stripping
// a single top-level directory if the zip was created with one), then returns.
// If no zip is present it is a no-op.
func extractSubmissionZip(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
			return unzipTo(filepath.Join(dir, e.Name()), dir)
		}
	}
	return nil
}

// unzipTo extracts src into dest, stripping a common single top-level directory
// prefix when all entries share one (e.g. zipping a folder rather than its contents).
func unzipTo(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	prefix := zipCommonPrefix(r.File)
	destClean := filepath.Clean(dest) + string(os.PathSeparator)

	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		// Zip-slip guard
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destClean) {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// zipCommonPrefix returns the single directory prefix shared by all entries, or "".
func zipCommonPrefix(files []*zip.File) string {
	prefix := ""
	for _, f := range files {
		name := filepath.ToSlash(f.Name)
		if name == "" {
			continue
		}
		idx := strings.Index(name, "/")
		if idx == -1 {
			return "" // file at root — no common prefix
		}
		top := name[:idx+1]
		if prefix == "" {
			prefix = top
		} else if prefix != top {
			return "" // multiple top-level dirs
		}
	}
	return prefix
}

// createTarContext builds a tar archive from a directory for Docker build context.
func createTarContext(dir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Normalize path separators for Docker (Linux)
		rel = strings.ReplaceAll(rel, "\\", "/")

		if rel == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return &buf, nil
}
