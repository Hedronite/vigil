package exitcode

import "errors"

// Error is a failure that knows its exit code and its stable machine code.
// Backend packages return it, and the command layer prints it as one
// vigil.error/v0 line. The message, when set, is fixed text written by Vigil:
// it never carries a URL, an address, a token or an echo of caller input.
type Error struct {
	Exit    int
	Code    string
	Message string
}

// Error implements error. It prints the code alone, so wrapping an Error in a
// log line cannot leak more than the code.
func (e *Error) Error() string { return e.Code }

// New returns an Error with no message.
func New(exit int, code string) *Error { return &Error{Exit: exit, Code: code} }

// Newf returns an Error with fixed message text.
func Newf(exit int, code, message string) *Error {
	return &Error{Exit: exit, Code: code, Message: message}
}

// As reports the Error inside err, if there is one.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
