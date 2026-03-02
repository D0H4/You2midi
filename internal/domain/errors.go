package domain

import "fmt"

// EngineError is a structured error returned by engine adapters and the runner.
type EngineError struct {
	Code             ErrorCode
	Message          string
	Retryable        bool
	FallbackStrategy string // e.g. "cpu" — empty means no fallback
	Cause            error
}

func (e *EngineError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *EngineError) Unwrap() error { return e.Cause }

// ErrorCode is a machine-readable error identifier.
type ErrorCode string

const (
	ErrEngineNotFound   ErrorCode = "ENGINE_NOT_FOUND"
	ErrModelMissing     ErrorCode = "MODEL_MISSING"
	ErrOOM              ErrorCode = "OOM"
	ErrTimeout          ErrorCode = "TIMEOUT"
	ErrCorruptFile      ErrorCode = "CORRUPT_FILE"
	ErrCopyrightBlocked ErrorCode = "COPYRIGHT_BLOCKED"
	ErrCancelled        ErrorCode = "CANCELLED"
	ErrRetryExhausted   ErrorCode = "RETRY_EXHAUSTED"
	ErrTransientCrash   ErrorCode = "TRANSIENT_CRASH"
	ErrUnknown          ErrorCode = "UNKNOWN"
)

// errorMeta maps error codes to retry policy.
var errorMeta = map[ErrorCode]struct {
	Retryable        bool
	FallbackStrategy string
	UserMessage      string
}{
	ErrEngineNotFound:   {false, "", "Engine binary not found. Run: pip install transkun"},
	ErrModelMissing:     {false, "", "Model file not found. Re-run health check."},
	ErrOOM:              {true, "cpu", "GPU out of memory. Retrying on CPU…"},
	ErrTimeout:          {true, "", "Processing timed out. Retrying…"},
	ErrCorruptFile:      {false, "", "Audio file is corrupt or unsupported."},
	ErrCopyrightBlocked: {false, "", "Video is not available for download."},
	ErrCancelled:        {false, "", "Cancelled."},
	ErrRetryExhausted:   {false, "", "Failed after maximum retry attempts."},
	ErrTransientCrash:   {true, "", "Engine crashed unexpectedly. Retrying…"},
	ErrUnknown:          {false, "", "An unexpected error occurred."},
}

// NewEngineError constructs a structured error for the given code.
func NewEngineError(code ErrorCode, cause error) *EngineError {
	meta := errorMeta[code]
	return &EngineError{
		Code:             code,
		Message:          meta.UserMessage,
		Retryable:        meta.Retryable,
		FallbackStrategy: meta.FallbackStrategy,
		Cause:            cause,
	}
}

// UserMessage returns the human-readable message for an error code.
func UserMessage(code ErrorCode) string {
	return errorMeta[code].UserMessage
}
