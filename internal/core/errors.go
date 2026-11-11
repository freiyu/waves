package core

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a failure the API shows to the user: a stable code, what happened,
// and what to do next. Messages never contain secrets.
type Error struct {
	Status   int
	Code     string
	Message  string
	NextStep string
	Extra    map[string]int // attempts_left, retry_after_s, record
	cause    error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return e.Code + ": " + e.Message + ": " + e.cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

func fail(status int, code, msg, next string) *Error {
	return &Error{Status: status, Code: code, Message: msg, NextStep: next}
}

func (e *Error) with(k string, v int) *Error {
	if e.Extra == nil {
		e.Extra = map[string]int{}
	}
	e.Extra[k] = v
	return e
}

// AsError returns the *Error inside err, or nil.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

var (
	errNotFound = func(what, next string) *Error {
		return fail(http.StatusNotFound, "not_found", "No such "+what+".", next)
	}
	errInvalidState = func(msg, next string) *Error { return fail(http.StatusConflict, "invalid_state", msg, next) }
	errValidation   = func(msg, next string) *Error {
		return fail(http.StatusUnprocessableEntity, "validation_failed", msg, next)
	}
	errStepUp = func(msg string) *Error {
		return fail(http.StatusForbidden, "stepup_required", msg, "Enter the approval passphrase and try again.")
	}
	errWrongScope = fail(http.StatusForbidden, "wrong_scope", "This credential cannot do that.", "Use the dashboard or the CLI as the human operator.")
)

func wrap(err error, what string, a ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(what+": %w", append(a, err)...)
}
