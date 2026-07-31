package model

import (
	"fmt"
	"net/http"
)

type AppError struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	ErrorKey string `json:"error_key,omitempty"`
	Details  string `json:"details,omitempty"`
	Cause    error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Cause
}

func (e *AppError) Is(target error) bool {
	t, ok := target.(*AppError)
	return ok && e.Code == t.Code
}

func NewAppError(code int, message string, cause error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

func NewAppErrorKey(code int, errorKey, message string, cause error) *AppError {
	return &AppError{
		Code:     code,
		Message:  message,
		ErrorKey: errorKey,
		Cause:    cause,
	}
}

func NewBadRequest(message string, cause error) *AppError {
	return NewAppError(http.StatusBadRequest, message, cause)
}

func NewBadRequestKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusBadRequest, errorKey, message, cause)
}

func NewForbidden(message string, cause error) *AppError {
	return NewAppError(http.StatusForbidden, message, cause)
}

func NewForbiddenKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusForbidden, errorKey, message, cause)
}

func NewNotFound(message string, cause error) *AppError {
	return NewAppError(http.StatusNotFound, message, cause)
}

func NewNotFoundKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusNotFound, errorKey, message, cause)
}

func NewConflict(message string, cause error) *AppError {
	return NewAppError(http.StatusConflict, message, cause)
}

func NewInternal(message string, cause error) *AppError {
	return NewAppError(http.StatusInternalServerError, message, cause)
}

func NewInternalKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusInternalServerError, errorKey, message, cause)
}

func NewServiceUnavailable(message string, cause error) *AppError {
	return NewAppError(http.StatusServiceUnavailable, message, cause)
}

func NewServiceUnavailableKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusServiceUnavailable, errorKey, message, cause)
}

func NewUnauthorizedKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusUnauthorized, errorKey, message, cause)
}

func NewTooManyRequestsKey(errorKey, message string, cause error) *AppError {
	return NewAppErrorKey(http.StatusTooManyRequests, errorKey, message, cause)
}
