package model

import (
	"errors"
	"net/http"
	"testing"
)

func TestNewAppError(t *testing.T) {
	msg := "test error"
	cause := errors.New("root cause")
	e := NewAppError(http.StatusBadRequest, msg, cause)

	if e.Code != http.StatusBadRequest {
		t.Errorf("expected code %d, got %d", http.StatusBadRequest, e.Code)
	}
	if e.Message != msg {
		t.Errorf("expected message %q, got %q", msg, e.Message)
	}
	if e.Cause != cause {
		t.Errorf("expected cause %v, got %v", cause, e.Cause)
	}
}

func TestAppError_Error(t *testing.T) {
	t.Run("with cause", func(t *testing.T) {
		cause := errors.New("root cause")
		e := NewAppError(400, "bad request", cause)
		expected := "bad request: root cause"
		if e.Error() != expected {
			t.Errorf("expected %q, got %q", expected, e.Error())
		}
	})

	t.Run("without cause", func(t *testing.T) {
		e := NewAppError(400, "bad request", nil)
		expected := "bad request"
		if e.Error() != expected {
			t.Errorf("expected %q, got %q", expected, e.Error())
		}
	})
}

func TestAppError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	e := NewAppError(400, "bad request", cause)
	if err := errors.Unwrap(e); err != cause {
		t.Errorf("expected unwrapped cause %v, got %v", cause, err)
	}
}

func TestAppError_Is(t *testing.T) {
	e1 := NewAppError(400, "error 1", nil)
	e2 := NewAppError(400, "error 2", nil)
	e3 := NewAppError(500, "error 3", nil)

	if !e1.Is(e2) {
		t.Error("expected same code errors to match")
	}
	if e1.Is(e3) {
		t.Error("expected different code errors to not match")
	}
	if e1.Is(errors.New("plain error")) {
		t.Error("expected plain error to not match")
	}
}

func TestFactoryFunctions(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string, error) *AppError
		code int
	}{
		{"NewBadRequest", NewBadRequest, http.StatusBadRequest},
		{"NewForbidden", NewForbidden, http.StatusForbidden},
		{"NewNotFound", NewNotFound, http.StatusNotFound},
		{"NewConflict", NewConflict, http.StatusConflict},
		{"NewInternal", NewInternal, http.StatusInternalServerError},
		{"NewServiceUnavailable", NewServiceUnavailable, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.fn("test", nil)
			if e.Code != tt.code {
				t.Errorf("expected code %d, got %d", tt.code, e.Code)
			}
			if e.Message != "test" {
				t.Errorf("expected message %q, got %q", "test", e.Message)
			}
		})
	}
}

func TestErrorsAs(t *testing.T) {
	cause := NewBadRequest("bad", errors.New("root"))
	var appErr *AppError
	if !errors.As(cause, &appErr) {
		t.Error("expected errors.As to succeed")
	}
	if appErr.Code != http.StatusBadRequest {
		t.Errorf("expected code %d, got %d", http.StatusBadRequest, appErr.Code)
	}
}
