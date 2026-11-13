// Package apperr 定义结构化业务错误。
//
// 约定：业务拒绝一律用 4xx + 稳定错误码，handler 只做映射，不自行编造状态码。
package apperr

import "fmt"

// Error 是可映射为 HTTP 响应的业务错误。
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string {
	if len(e.Details) == 0 {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s %v", e.Code, e.Message, e.Details)
}

// New 构造业务错误。
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// WithDetails 附加结构化上下文（如当前版本号），返回自身便于链式调用。
func (e *Error) WithDetails(kv map[string]any) *Error {
	if e.Details == nil {
		e.Details = make(map[string]any, len(kv))
	}
	for k, v := range kv {
		e.Details[k] = v
	}
	return e
}

// 常用错误构造器。
func Forbidden(message string) *Error {
	return New(403, "forbidden_role", message)
}

func NotFound(message string) *Error {
	return New(404, "not_found", message)
}

func Invalid(message string) *Error {
	return New(400, "invalid_input", message)
}

func Conflict(code, message string) *Error {
	return New(409, code, message)
}
