package ytdlp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"you2midi/internal/runner"
)

type staticRunner struct {
	res *runner.RunResult
	err error
}

func (r *staticRunner) Run(_ context.Context, _ string, _ []string, _ runner.RunOptions) (*runner.RunResult, error) {
	return r.res, r.err
}

type fallbackRunner struct {
	call int
}

func (r *fallbackRunner) Run(_ context.Context, name string, _ []string, opts runner.RunOptions) (*runner.RunResult, error) {
	r.call++
	if r.call == 1 {
		return nil, fmt.Errorf("runner: start %q: executable not found", name)
	}
	audioPath := filepath.Join(opts.Dir, "audio.mp3")
	if err := os.WriteFile(audioPath, []byte("x"), 0o644); err != nil {
		return nil, err
	}
	return &runner.RunResult{ExitCode: 0}, nil
}

func TestIsHardError(t *testing.T) {
	t.Parallel()
	if !isHardError("Video unavailable", "") {
		t.Fatal("expected hard error for unavailable video")
	}
	if isHardError("js challenge warning", "") {
		t.Fatal("did not expect hard error for non-fatal warning")
	}
}

func TestFindAudioFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := findAudioFile(dir)
	if err != nil {
		t.Fatalf("findAudioFile: %v", err)
	}
	if got != p {
		t.Fatalf("expected %s, got %s", p, got)
	}
}

func TestBuildArgs_IncludesNodeRuntimeWhenAvailable(t *testing.T) {
	t.Parallel()
	d := &Downloader{bin: "yt-dlp", nodeBin: "/usr/bin/node"}
	args := d.buildArgs("out.%(ext)s", "https://youtube.com/watch?v=test")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--js-runtimes node:/usr/bin/node") {
		t.Fatalf("expected node runtime args, got %q", joined)
	}
}

func TestDownloadAndHash_ReturnsFileHash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := []byte("audio-bytes")
	audioPath := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(audioPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d := &Downloader{
		bin:    "yt-dlp",
		runner: &staticRunner{res: &runner.RunResult{ExitCode: 0}},
	}

	gotPath, gotHash, err := d.DownloadAndHash(context.Background(), "https://youtube.com/watch?v=test", dir)
	if err != nil {
		t.Fatalf("DownloadAndHash: %v", err)
	}
	if gotPath != audioPath {
		t.Fatalf("expected audio path %s, got %s", audioPath, gotPath)
	}
	sum := sha256.Sum256(data)
	wantHash := hex.EncodeToString(sum[:])
	if gotHash != wantHash {
		t.Fatalf("expected hash %s, got %s", wantHash, gotHash)
	}
}

func TestDownload_FallsBackToDefaultBinaryOnLaunchFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := &fallbackRunner{}
	d := &Downloader{
		bin:    "C:/broken/runtime/yt-dlp.exe",
		runner: r,
	}

	audioPath, err := d.Download(context.Background(), "https://youtube.com/watch?v=test", dir)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.HasSuffix(audioPath, "audio.mp3") {
		t.Fatalf("unexpected audio path: %s", audioPath)
	}
	if d.bin != "yt-dlp" {
		t.Fatalf("expected downloader to switch to fallback bin yt-dlp, got %s", d.bin)
	}
}

func TestNew_NormalizesRelativePathLikeBinaryToAbsolute(t *testing.T) {
	t.Parallel()
	d := New(".venv/Scripts/yt-dlp.exe", &staticRunner{})
	if !filepath.IsAbs(d.bin) {
		t.Fatalf("expected absolute binary path, got %q", d.bin)
	}
	if !strings.HasSuffix(strings.ToLower(d.bin), strings.ToLower(filepath.FromSlash(".venv/Scripts/yt-dlp.exe"))) {
		t.Fatalf("unexpected normalized path: %q", d.bin)
	}
}

func TestNew_DoesNotRewritePlainCommandName(t *testing.T) {
	t.Parallel()
	d := New("yt-dlp", &staticRunner{})
	if d.bin != "yt-dlp" {
		t.Fatalf("expected plain command name to remain unchanged, got %q", d.bin)
	}
}
