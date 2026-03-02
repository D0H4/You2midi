package service

import "errors"

var (
	ErrQueueFull  = errors.New("queue is full")
	ErrJobMissing = errors.New("job not found")
)
