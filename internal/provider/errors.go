package provider

import (
	"errors"

	"github.com/ledatu/csar-notify/internal/domain"
)

type SendError struct {
	Channel   domain.Channel
	Permanent bool
	Err       error
}

func (e *SendError) Error() string {
	if e == nil || e.Err == nil {
		return "provider send failed"
	}
	return e.Err.Error()
}

func (e *SendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func PermanentSendError(channel domain.Channel, err error) error {
	if err == nil {
		return nil
	}
	return &SendError{Channel: channel, Permanent: true, Err: err}
}

func TemporarySendError(channel domain.Channel, err error) error {
	if err == nil {
		return nil
	}
	return &SendError{Channel: channel, Err: err}
}

func IsPermanentChannelOnly(err error, channel domain.Channel) bool {
	if err == nil {
		return false
	}

	matched := false
	only := true
	walkLeafErrors(err, func(leaf error) {
		var sendErr *SendError
		if !errors.As(leaf, &sendErr) || sendErr.Channel != channel || !sendErr.Permanent {
			only = false
			return
		}
		matched = true
	})

	return only && matched
}

type singleUnwrapper interface {
	Unwrap() error
}

type multiUnwrapper interface {
	Unwrap() []error
}

func walkLeafErrors(err error, visit func(error)) {
	if sendErr, ok := err.(*SendError); ok {
		visit(sendErr)
		return
	}

	switch e := err.(type) {
	case multiUnwrapper:
		children := e.Unwrap()
		if len(children) == 0 {
			visit(err)
			return
		}
		for _, child := range children {
			if child != nil {
				walkLeafErrors(child, visit)
			}
		}
	case singleUnwrapper:
		child := e.Unwrap()
		if child == nil {
			visit(err)
			return
		}
		walkLeafErrors(child, visit)
	default:
		visit(err)
	}
}
