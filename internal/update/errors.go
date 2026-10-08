package update

import (
	"errors"
	"fmt"
)

// Kind classifies an *Error so that the command can choose an exit code and
// the right advice without parsing messages.
type Kind int

// Error kinds.
const (
	// KindFailure is an update that could not be completed (network,
	// download, verification, disk). Nothing was replaced.
	KindFailure Kind = iota
	// KindVerify is a verification failure: checksum, signature, archive
	// shape or the downloaded binary's own version.
	KindVerify
	// KindUsage is a request that cannot be honored as asked (a downgrade
	// without --allow-downgrade, a malformed version).
	KindUsage
	// KindManaged is a binary that a package manager owns or a development
	// build. --force overrides it.
	KindManaged
	// KindNotWritable is a directory the user cannot write.
	KindNotWritable
	// KindSignatureRequired is --require-signature without a usable cosign.
	KindSignatureRequired
	// KindLocked is another update in progress.
	KindLocked
	// KindNoBackup is a rollback without a previous binary.
	KindNoBackup
)

// Error is an update failure with advice.
type Error struct {
	Kind Kind
	Msg  string
	// HintText tells the user what to do next.
	HintText string
	Err      error
}

// Error implements error.
func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

// Hint returns the advice shown after the error.
func (e *Error) Hint() string { return e.HintText }

func newErr(kind Kind, hint string, err error, format string, a ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, a...), HintText: hint, Err: err}
}

// KindOf returns the Kind of err, or KindFailure when it is not an *Error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindFailure
}
