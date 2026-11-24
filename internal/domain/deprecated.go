package domain

import "errors"

// ErrZCodeRemoved explains how to migrate configurations from the retired backend.
var ErrZCodeRemoved = errors.New("zcode backend is deprecated and removed; use codex, cursor, kimi, opencode1, opencode2, or devin and start a fresh Squad session")
