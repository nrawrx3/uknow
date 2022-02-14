package admin

import (
	"fmt"
	"net/http"
)

type SendEventMessageFailedError struct {
	PlayerName string
	EventName  string
	Reason     error
}

func NewSendEventMessageFailedError(playerName string, eventName string, reason error) *SendEventMessageFailedError {
	return &SendEventMessageFailedError{
		PlayerName: playerName,
		EventName:  eventName,
		Reason:     reason,
	}
}

func (e *SendEventMessageFailedError) Unwrap() error {
	return e.Reason
}

func (e *SendEventMessageFailedError) Error() string {
	reason := ""
	if e.Reason != nil {
		reason = fmt.Sprintf("Reason: %s", e.Reason.Error())
	}
	return fmt.Sprintf("Failed to send event message '%s' to player '%s'. %s", e.EventName, e.PlayerName, reason)
}

type HTTPResponseCodeError struct {
	StatusCode int
	Status     string
}

func NewHTTPResponseCodeError(StatusCode int) *HTTPResponseCodeError {
	return &HTTPResponseCodeError{
		StatusCode: StatusCode,
		Status:     http.StatusText(StatusCode),
	}
}

func (e *HTTPResponseCodeError) Error() string {
	return fmt.Sprintf("HTTP error response: %d (%s)", e.StatusCode, e.Status)
}
