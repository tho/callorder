// Package initfirst puts init first, like decorder's init-first check.
package initfirst

var registry = map[string]func(){}

// Run runs the registered function.
func Run() { registry["run"]() } // want `expected init before Run: init functions come first \(decorder init-first\)`

func register() { registry["run"] = Run }

func init() { register() }
