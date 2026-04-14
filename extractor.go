package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// KiCadAssets holds the paths to the extracted files we care about
type KiCadAssets struct {
	SymbolPath    string
	FootprintPath string
	ModelPath     string
	SchBlockPath  string
	PcbBlockPath  string
}

// PeekForKiCad checks the zip headers WITHOUT extracting to see if it's relevant
func PeekForKiCad(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))

	// If it's not a zip, handle other valid standalone formats here
	if ext == ".epw" {
		return true // Usually valid CAD files, pass through
	}
	if ext != ".zip" {
		return false
	}

	// --- Memory Buffer Approach ---
	// Read into memory instantly to avoid locking the file while the browser is handling it
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false // Fails gracefully if the zip is actively downloading and incomplete
	}

	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			innerExt := strings.ToLower(filepath.Ext(f.Name))
			switch innerExt {
			case ".kicad_sym", ".kicad_mod", ".step", ".stp", ".wrl", ".kicad_sch", ".kicad_pcb":
				return true
			}
		}
	}
	return false
}

// ExtractAndFind processes the zip file, extracts it to a temp dir, and locates the KiCad assets
func ExtractAndFind(zipPath string) (*KiCadAssets, string, error) {
	tempDir, err := os.MkdirTemp("", "kicad-manager-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	err = unzip(zipPath, tempDir)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, "", fmt.Errorf("failed to unzip: %w", err)
	}

	assets := &KiCadAssets{}
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			switch ext {
			case ".kicad_sym":
				assets.SymbolPath = path
			case ".kicad_mod":
				assets.FootprintPath = path
			case ".step", ".stp", ".wrl":
				assets.ModelPath = path
			case ".kicad_sch":
				assets.SchBlockPath = path
			case ".kicad_pcb":
				assets.PcbBlockPath = path
			}
		}
		return nil
	})

	if err != nil {
		os.RemoveAll(tempDir)
		return nil, "", fmt.Errorf("failed to scan temp dir: %w", err)
	}

	return assets, tempDir, nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, ioErr := io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if ioErr != nil {
			return ioErr
		}
	}
	return nil
}
