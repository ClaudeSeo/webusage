package credfinder

import "errors"

var (
	// ErrNotSupported는 현재 플랫폼에서 지원하지 않는 기능에 반환됩니다
	ErrNotSupported = errors.New("credfinder: not supported on this platform")
	// ErrNotFound는 요청한 자격증명이 존재하지 않을 때 반환됩니다
	ErrNotFound = errors.New("credfinder: credential not found")
)
