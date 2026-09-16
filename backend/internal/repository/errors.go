package repository

import "errors"

// 仓储层哨兵错误。
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate record")

	// 资金台账专用错误。
	ErrInsufficient          = errors.New("insufficient fund balance")
	ErrAlreadyReversed       = errors.New("fund entry already reversed")
	ErrCannotReverseReversal = errors.New("cannot reverse a reversal entry")
	ErrConflict              = errors.New("fund concurrent conflict")
)
