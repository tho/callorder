// Package function puts exported functions first, like funcorder's function check.
package function

func helper() {} // want `expected Serve before helper: exported functions come before unexported functions \(funcorder function\)`

// Serve serves.
func Serve() {
	helper()
	route()
}

func route() {}

// Close closes.
func Close() {}
