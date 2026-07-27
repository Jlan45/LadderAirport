package dnsprovider

import (
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorAuthentication ErrorKind = "authentication"
	ErrorPermission     ErrorKind = "permission"
	ErrorRateLimit      ErrorKind = "rate_limit"
	ErrorTransient      ErrorKind = "transient"
	ErrorPermanent      ErrorKind = "permanent"
)

type ProviderError struct {
	Kind              ErrorKind
	Provider          string
	Operation         string
	Message           string
	RetryAfterSeconds int
	Cause             error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	prefix := e.Provider
	if e.Operation != "" {
		if prefix != "" {
			prefix += " "
		}
		prefix += e.Operation
	}
	if prefix == "" {
		return e.Message
	}
	if e.Message == "" {
		return prefix + " 失败"
	}
	return fmt.Sprintf("%s 失败：%s", prefix, e.Message)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func ErrorKindOf(err error) ErrorKind {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Kind
	}
	return ErrorTransient
}

func IsRetryable(err error) bool {
	switch ErrorKindOf(err) {
	case ErrorRateLimit, ErrorTransient:
		return true
	default:
		return false
	}
}
