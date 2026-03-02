package common

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"you2midi/internal/domain"
)

var oomPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)out of memory`),
	regexp.MustCompile(`(?i)CUDA error.*out of memory`),
	regexp.MustCompile(`(?i)RuntimeError.*CUDA`),
}

// WriteStream writes r to the destination path.
func WriteStream(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// CleanupReader removes dir after the underlying stream is closed.
type CleanupReader struct {
	io.ReadCloser
	Dir string
}

func (c *CleanupReader) Close() error {
	err := c.ReadCloser.Close()
	_ = os.RemoveAll(c.Dir)
	return err
}

// ClassifyStderr maps CLI stderr to domain error codes.
func ClassifyStderr(stderr []byte, detectOOM bool) *domain.EngineError {
	text := string(stderr)
	if detectOOM {
		for _, re := range oomPatterns {
			if re.MatchString(text) {
				return domain.NewEngineError(domain.ErrOOM, fmt.Errorf("%s", bytes.TrimSpace(stderr)))
			}
		}
	}
	if strings.Contains(text, "No such file") || strings.Contains(text, "not found") {
		return domain.NewEngineError(domain.ErrEngineNotFound, fmt.Errorf("%s", bytes.TrimSpace(stderr)))
	}
	return domain.NewEngineError(domain.ErrUnknown, fmt.Errorf("%s", bytes.TrimSpace(stderr)))
}

// WrapRunError maps context/process errors to domain-level run errors.
func WrapRunError(err error) *domain.EngineError {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "context canceled") {
		return domain.NewEngineError(domain.ErrCancelled, err)
	}
	if strings.Contains(msg, "context deadline exceeded") {
		return domain.NewEngineError(domain.ErrTimeout, err)
	}
	return domain.NewEngineError(domain.ErrTransientCrash, err)
}
