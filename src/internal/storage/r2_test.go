package storage

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("boom"), false},
		{"internal error", &smithy.GenericAPIError{Code: "InternalError"}, true},
		{"slow down", &smithy.GenericAPIError{Code: "SlowDown"}, true},
		{"access denied", &smithy.GenericAPIError{Code: "AccessDenied"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err); got != tt.want {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}
