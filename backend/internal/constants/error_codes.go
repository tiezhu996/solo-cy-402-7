package constants

// 统一错误码：0 表示成功，其余按模块分段。
const (
	CodeOK                    = 0
	CodeBadRequest            = 40000
	CodeUnauthorized          = 40100
	CodeForbidden             = 40300
	CodeNotFound              = 40400
	CodeConflict              = 40900
	CodeTooManyRequests       = 42900
	CodeValidationFailed      = 42200
	CodeInternalError         = 50000
	CodeUserExists            = 40001
	CodeInvalidCredentials    = 40101
	CodeCaseStatusConflict    = 40901
	CodeBillingStatusConflict = 40902
	CodeFundInsufficient      = 40903 // 专案可用余额不足，支出整笔拒绝
	CodeFundAlreadyReversed   = 40904 // 明细已被冲销，禁止重复冲销
	CodeFundConflict          = 40905 // 资金台账并发冲突
	CodeUploadTooLarge        = 41300
	CodeUnsupportedType       = 41500
)
