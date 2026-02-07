package builder

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

// ZIPProcessor handles ZIP package extraction and processing
type ZIPProcessor struct {
	workDir string
}

// NewZIPProcessor creates a new ZIP processor
func NewZIPProcessor(workDir string) *ZIPProcessor {
	return &ZIPProcessor{
		workDir: workDir,
	}
}

// Process extracts and validates a ZIP package
func (zp *ZIPProcessor) Process(ctx context.Context, packageData []byte, functionID string) (string, error) {
	logger.Info("Processing ZIP package", "function_id", functionID, "size_bytes", len(packageData))

	// Create function-specific directory
	funcDir := filepath.Join(zp.workDir, functionID)
	if err := os.MkdirAll(funcDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create function directory: %w", err)
	}

	// Extract ZIP
	if err := zp.extractZIP(packageData, funcDir); err != nil {
		return "", fmt.Errorf("failed to extract ZIP: %w", err)
	}

	logger.Info("ZIP package extracted successfully", "function_id", functionID, "path", funcDir)
	return funcDir, nil
}

// extractZIP extracts a ZIP archive to the target directory
func (zp *ZIPProcessor) extractZIP(data []byte, targetDir string) error {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return fmt.Errorf("failed to read ZIP: %w", err)
	}

	for _, file := range zipReader.File {
		if err := zp.extractFile(file, targetDir); err != nil {
			return err
		}
	}

	return nil
}

// extractFile extracts a single file from ZIP
func (zp *ZIPProcessor) extractFile(file *zip.File, targetDir string) error {
	// Construct target path
	targetPath := filepath.Join(targetDir, file.Name)

	// Prevent directory traversal attacks
	if !filepath.HasPrefix(targetPath, filepath.Clean(targetDir)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", file.Name)
	}

	// Create directory if needed
	if file.FileInfo().IsDir() {
		return os.MkdirAll(targetPath, file.Mode())
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	// Open source file
	srcFile, err := file.Open()
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Copy contents
	_, err = io.Copy(dstFile, srcFile)
	return err
}

// Cleanup removes the extracted directory
func (zp *ZIPProcessor) Cleanup(functionID string) error {
	funcDir := filepath.Join(zp.workDir, functionID)
	return os.RemoveAll(funcDir)
}

// ValidatePackage performs basic validation on the package
func (zp *ZIPProcessor) ValidatePackage(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("package data is empty")
	}

	// Check if it's a valid ZIP
	reader := bytes.NewReader(data)
	_, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return fmt.Errorf("invalid ZIP file: %w", err)
	}

	return nil
}
