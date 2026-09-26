// Package disabled turns off funcorder's constructor and struct-method checks.
package disabled

// T is a type.
type T struct{}

func (T) internal() {}

// Public is exported.
func (T) Public() {}

// NewT returns a T.
func NewT() T { return T{} }
